package probe_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/agent/internal/controlplane"
	"github.com/araihu/xisnove/agent/probe"
)

func TestTCPExecutorSendsAndMatchesBoundedResponse(t *testing.T) {
	address := serveTCP(t, func(connection net.Conn) {
		request := make([]byte, 4)
		_, _ = io.ReadFull(connection, request)
		if string(request) == "PING" {
			_, _ = connection.Write([]byte("prefix-PONG-suffix"))
		}
	})

	result := probe.NewTCPExecutor(tcpLoopbackPolicy()).Execute(
		context.Background(),
		tcpWork(address, []byte("PING"), []byte("PONG"), 5000, nil),
	)
	if result.Outcome != controlplane.Passed ||
		result.ErrorCode != controlplane.Empty ||
		result.ProtocolTimings == nil ||
		result.ProtocolTimings.ConnectMillis == nil {
		t.Fatalf("result = %#v", result)
	}
}

func TestTCPExecutorReportsExpectationMismatch(t *testing.T) {
	address := serveTCP(t, func(connection net.Conn) {
		_, _ = connection.Write([]byte("NOPE"))
	})

	result := probe.NewTCPExecutor(tcpLoopbackPolicy()).Execute(
		context.Background(),
		tcpWork(address, nil, []byte("PONG"), 5000, nil),
	)
	if result.Outcome != controlplane.Failed ||
		result.ErrorCode != controlplane.TcpExpectMismatch {
		t.Fatalf("result = %#v", result)
	}
}

func TestTCPExecutorReportsTimeout(t *testing.T) {
	address := serveTCP(t, func(connection net.Conn) {
		_, _ = io.Copy(io.Discard, connection)
	})

	result := probe.NewTCPExecutor(tcpLoopbackPolicy()).Execute(
		context.Background(),
		tcpWork(address, nil, []byte("PONG"), 50, nil),
	)
	if result.ErrorCode != controlplane.Timeout {
		t.Fatalf("result = %#v", result)
	}
}

func TestTCPExecutorDeniesPrivateTargetWithoutAllowList(t *testing.T) {
	address := serveTCP(t, func(net.Conn) {})
	result := probe.NewTCPExecutor(probe.DefaultPolicy()).Execute(
		context.Background(),
		tcpWork(address, nil, nil, 5000, nil),
	)
	if result.ErrorCode != controlplane.TargetDenied {
		t.Fatalf("result = %#v", result)
	}
}

func TestTCPExecutorObservesTLSCertificateExpiry(t *testing.T) {
	server := httptest.NewTLSServer(nil)
	t.Cleanup(server.Close)
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	address := strings.TrimPrefix(server.URL, "https://")
	minimumRemaining := int64(1)
	executor := probe.NewTCPExecutor(tcpLoopbackPolicy()).WithTLSConfig(&tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	})

	result := executor.Execute(
		context.Background(),
		tcpWork(address, nil, nil, 5000, &minimumRemaining),
	)
	if result.Outcome != controlplane.Passed ||
		result.TlsNotAfter == nil ||
		!result.TlsNotAfter.Equal(server.Certificate().NotAfter) ||
		result.ProtocolTimings == nil ||
		result.ProtocolTimings.TlsMillis == nil {
		t.Fatalf("result = %#v", result)
	}
}

func tcpLoopbackPolicy() probe.Policy {
	policy := probe.DefaultPolicy()
	policy.AllowedPrivate = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	return policy
}

func serveTCP(t *testing.T, handle func(net.Conn)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		handle(connection)
	}()
	return listener.Addr().String()
}

func tcpWork(
	address string,
	send []byte,
	expect []byte,
	timeoutMillis int32,
	tlsMinimumRemainingSeconds *int64,
) controlplane.ProbeWork {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		panic(err)
	}
	port, err := net.LookupPort("tcp", rawPort)
	if err != nil {
		panic(err)
	}
	var definition controlplane.ProbeDefinition
	if err := definition.FromTCPProbeDefinition(controlplane.TCPProbeDefinition{
		Host: host, Port: int32(port),
		Send: append([]byte{}, send...), Expect: append([]byte{}, expect...),
		TlsMinimumRemainingSeconds: tlsMinimumRemainingSeconds,
	}); err != nil {
		panic(err)
	}
	return controlplane.ProbeWork{
		RunId:      uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		MonitorId:  uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		LeaseToken: "lease-token", ScheduledFor: time.Now().UTC(),
		TimeoutMillis: timeoutMillis, Probe: definition,
	}
}
