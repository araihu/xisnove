package probe_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/agent/internal/controlplane"
	"github.com/araihu/xisnove/agent/probe"
)

func TestHTTPExecutorEvaluatesStatusAndBody(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "booting")
	}))
	defer target.Close()

	executor := probe.NewHTTPExecutor(loopbackPolicy())
	result := executor.Execute(
		context.Background(),
		testWork(target.URL, http.StatusOK, "ready"),
	)
	if result.Outcome != controlplane.Failed {
		t.Fatalf("Outcome = %s", result.Outcome)
	}
	if result.ObservedStatus != http.StatusServiceUnavailable {
		t.Fatalf("ObservedStatus = %d", result.ObservedStatus)
	}
	if result.BodyAssertionPassed {
		t.Fatal("BodyAssertionPassed = true")
	}
	if result.ErrorCode != controlplane.StatusMismatch {
		t.Fatalf("ErrorCode = %q", result.ErrorCode)
	}
}

func TestHTTPExecutorPassesMatchingResponse(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ready")
	}))
	defer target.Close()

	result := probe.NewHTTPExecutor(loopbackPolicy()).Execute(
		context.Background(),
		testWork(target.URL, http.StatusOK, "ready"),
	)
	if result.Outcome != controlplane.Passed ||
		result.ErrorCode != controlplane.Empty ||
		!result.BodyAssertionPassed {
		t.Fatalf("result = %#v", result)
	}
	if result.ResultId == uuid.Nil || result.StartedAt.IsZero() || result.FinishedAt.IsZero() {
		t.Fatalf("result metadata = %#v", result)
	}
}

func TestHTTPExecutorDeniesMetadataAddress(t *testing.T) {
	executor := probe.NewHTTPExecutor(probe.DefaultPolicy())
	result := executor.Execute(
		context.Background(),
		testWork("http://169.254.169.254/latest", http.StatusOK, ""),
	)
	if result.ErrorCode != controlplane.TargetDenied {
		t.Fatalf("result = %#v", result)
	}
}

func TestHTTPExecutorRevalidatesRedirectTarget(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(
			w,
			&http.Request{},
			"http://169.254.169.254/latest",
			http.StatusFound,
		)
	}))
	defer target.Close()
	work := testWork(target.URL, http.StatusOK, "")
	definition, err := work.Probe.AsHTTPProbeDefinition()
	if err != nil {
		t.Fatal(err)
	}
	definition.FollowRedirects = true
	if err := work.Probe.FromHTTPProbeDefinition(definition); err != nil {
		t.Fatal(err)
	}

	result := probe.NewHTTPExecutor(loopbackPolicy()).Execute(context.Background(), work)
	if result.ErrorCode != controlplane.TargetDenied {
		t.Fatalf("result = %#v", result)
	}
}

func TestHTTPExecutorRejectsOversizedResponse(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 32))
	}))
	defer target.Close()
	policy := loopbackPolicy()
	policy.MaxResponseBytes = 8

	result := probe.NewHTTPExecutor(policy).Execute(
		context.Background(),
		testWork(target.URL, http.StatusOK, ""),
	)
	if result.ErrorCode != controlplane.ResponseTooLarge {
		t.Fatalf("result = %#v", result)
	}
}

func TestHTTPExecutorReportsTLSExpiryAndProtocolTimings(t *testing.T) {
	certificate, leaf := expiringServerCertificate(t, time.Now().Add(time.Hour))
	var gotHeader string
	var gotBody string
	server := httptest.NewUnstartedServer(http.HandlerFunc(
		func(w http.ResponseWriter, request *http.Request) {
			gotHeader = request.Header.Get("X-Xisnove-Test")
			body, _ := io.ReadAll(request.Body)
			gotBody = string(body)
			_, _ = io.WriteString(w, "ready")
		},
	))
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	work := testWork(server.URL, http.StatusOK, "ready")
	definition, err := work.Probe.AsHTTPProbeDefinition()
	if err != nil {
		t.Fatal(err)
	}
	definition.Method = controlplane.POST
	definition.Headers = map[string]string{"X-Xisnove-Test": "present"}
	definition.Body = []byte("probe-body")
	definition.BodyDoesNotContain = []string{"forbidden"}
	minimumRemaining := int64((2 * time.Hour) / time.Second)
	definition.TlsMinimumRemainingSeconds = &minimumRemaining
	if err := work.Probe.FromHTTPProbeDefinition(definition); err != nil {
		t.Fatal(err)
	}
	executor := probe.NewHTTPExecutor(loopbackPolicy()).WithTLSConfig(&tls.Config{
		RootCAs: pool, MinVersion: tls.VersionTLS12,
	})

	result := executor.Execute(context.Background(), work)
	if result.ErrorCode != controlplane.TlsExpiring ||
		result.TlsNotAfter == nil ||
		result.ProtocolTimings == nil ||
		result.ProtocolTimings.ConnectMillis == nil ||
		result.ProtocolTimings.TlsMillis == nil ||
		result.ProtocolTimings.FirstByteMillis == nil ||
		gotHeader != "present" ||
		gotBody != "probe-body" {
		t.Fatalf("result=%#v header=%q body=%q", result, gotHeader, gotBody)
	}
}

func TestHTTPExecutorEvaluatesNegativeBodyAssertions(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ready but forbidden")
	}))
	t.Cleanup(target.Close)
	work := testWork(target.URL, http.StatusOK, "ready")
	definition, err := work.Probe.AsHTTPProbeDefinition()
	if err != nil {
		t.Fatal(err)
	}
	definition.BodyDoesNotContain = []string{"forbidden"}
	if err := work.Probe.FromHTTPProbeDefinition(definition); err != nil {
		t.Fatal(err)
	}
	result := probe.NewHTTPExecutor(loopbackPolicy()).Execute(context.Background(), work)
	if result.ErrorCode != controlplane.BodyMismatch || result.BodyAssertionPassed {
		t.Fatalf("result = %#v", result)
	}
}

func loopbackPolicy() probe.Policy {
	return probe.Policy{
		AllowedPrivate:   []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
		MaxResponseBytes: 64 << 10,
		MaxRedirects:     3,
	}
}

func testWork(url string, expectedStatus int, bodyContains string) controlplane.ProbeWork {
	var definition controlplane.ProbeDefinition
	contains := []string{}
	if bodyContains != "" {
		contains = append(contains, bodyContains)
	}
	if err := definition.FromHTTPProbeDefinition(controlplane.HTTPProbeDefinition{
		Method: controlplane.GET, Url: url, Headers: map[string]string{}, Body: []byte{},
		ExpectedStatus: []controlplane.StatusRange{{
			Minimum: int32(expectedStatus), Maximum: int32(expectedStatus),
		}},
		BodyContains: contains, BodyDoesNotContain: []string{},
	}); err != nil {
		panic(err)
	}
	return controlplane.ProbeWork{
		RunId:         uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		MonitorId:     uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		LeaseToken:    "lease-token",
		ScheduledFor:  time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC),
		TimeoutMillis: 5000,
		Probe:         definition,
	}
}

func expiringServerCertificate(
	t *testing.T,
	notAfter time.Time,
) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{
			Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, leaf
}
