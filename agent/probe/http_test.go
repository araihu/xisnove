package probe_test

import (
	"context"
	"io"
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
	work.Http.FollowRedirects = true

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

func loopbackPolicy() probe.Policy {
	return probe.Policy{
		AllowedPrivate:   []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
		MaxResponseBytes: 64 << 10,
		MaxRedirects:     3,
	}
}

func testWork(url string, expectedStatus int, bodyContains string) controlplane.HTTPWork {
	return controlplane.HTTPWork{
		RunId:         uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		MonitorId:     uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		LeaseToken:    "lease-token",
		ScheduledFor:  time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC),
		TimeoutMillis: 5000,
		Http: controlplane.HTTPProbe{
			Method:          controlplane.HTTPProbeMethodGET,
			Url:             url,
			ExpectedStatus:  int32(expectedStatus),
			BodyContains:    bodyContains,
			FollowRedirects: false,
		},
	}
}
