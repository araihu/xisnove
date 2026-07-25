package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	"github.com/araihu/xisnove/internal/adapters/observability"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestOperationalEndpoints(t *testing.T) {
	ready := true
	handler, err := NewHandler(HandlerConfig{
		Server: NewServer(ServerConfig{}),
		Ready: func(context.Context) error {
			if !ready {
				return errors.New("draining")
			}
			return nil
		},
		Metrics: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("xisnove_test_metric 1\n"))
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/livez", "/readyz"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("GET %s status = %d", path, response.Code)
		}
		if response.Header().Get("X-Request-ID") == "" {
			t.Fatalf("GET %s has no correlation ID", path)
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "xisnove_test_metric 1") {
		t.Fatalf("GET /metrics = %d %q", response.Code, response.Body.String())
	}

	ready = false
	request = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"not_ready"`) {
		t.Fatalf("draining GET /readyz = %d %q", response.Code, response.Body.String())
	}
}

func TestMetricsEndpointIsOptIn(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{Server: NewServer(ServerConfig{}), Ready: func(context.Context) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("GET /metrics without handler = %d", response.Code)
	}
}

func TestAgentWorkAdmissionRejectsDuringDrain(t *testing.T) {
	server, credential := newAdmissionTestServer(t)
	handler, err := NewHandler(HandlerConfig{
		Server: server, Ready: func(context.Context) error { return nil },
		AdmitWork: func(context.Context) (context.Context, func(), error) {
			return nil, nil, errors.New("draining")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/agent/work:lease", strings.NewReader(`{"waitSeconds":0,"capabilities":["http"]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+credential)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"not_ready"`) {
		t.Fatalf("draining work lease = %d %q", response.Code, response.Body.String())
	}
}

func TestAgentWorkAdmissionRunsOnlyAfterValidationAndAuthentication(t *testing.T) {
	server, credential := newAdmissionTestServer(t)
	var admissions atomic.Int32
	handler, err := NewHandler(HandlerConfig{
		Server: server,
		Ready:  func(context.Context) error { return nil },
		AdmitWork: func(ctx context.Context) (context.Context, func(), error) {
			admissions.Add(1)
			return ctx, func() {}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name          string
		body          string
		authorization string
		wantStatus    int
	}{
		{name: "invalid contract", body: `{}`, authorization: "Bearer " + credential, wantStatus: http.StatusBadRequest},
		{name: "unauthenticated", body: `{"waitSeconds":0,"capabilities":["http"]}`, wantStatus: http.StatusUnauthorized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := admissions.Load()
			request := httptest.NewRequest(http.MethodPost, "/v1/agent/work:lease", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			if test.authorization != "" {
				request.Header.Set("Authorization", test.authorization)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.wantStatus, response.Body)
			}
			if got := admissions.Load(); got != before {
				t.Fatalf("admissions = %d, want %d", got, before)
			}
		})
	}
}

func TestRequestAndPanicLogsContainCorrelationID(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(observability.NewJSONLogger(&output, observability.LogConfig{}))
	t.Cleanup(func() { slog.SetDefault(previous) })

	handler := correlationIDs(logRequests(recoverPanics(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("test panic")
	}))))
	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	request.Header.Set("X-Request-ID", "request-panic")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	logs := output.String()
	for _, message := range []string{"panic serving request", "HTTP request"} {
		line := logLineContaining(t, logs, message)
		if !strings.Contains(line, `"correlation_id":"request-panic"`) {
			t.Fatalf("%s log lacks correlation ID: %s", message, line)
		}
	}
}

func logLineContaining(t *testing.T, logs, message string) string {
	t.Helper()
	for line := range strings.SplitSeq(logs, "\n") {
		if strings.Contains(line, `"msg":"`+message+`"`) {
			return line
		}
	}
	t.Fatalf("logs lack %q: %s", message, logs)
	return ""
}

func newAdmissionTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "admission.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(db)
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	location, err := domain.NewLocation("11111111-1111-4111-8111-111111111111", "admission", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Repositories().Locations.Create(ctx, location); err != nil {
		t.Fatal(err)
	}
	agent, err := domain.NewAgent(domain.NewAgentParams{
		ID: "22222222-2222-4222-8222-222222222222", LocationID: location.ID,
		Name: "admission-agent", Capabilities: []domain.AgentCapability{domain.CapabilityHTTP},
		CredentialGeneration: 1, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	const credential = "admission-credential"
	tokens := xiscrypto.NewProductionTokenIssuer()
	if err := store.Repositories().Agents.Create(ctx, application.AgentRecord{
		Agent: agent, CredentialHash: tokens.Hash(credential),
	}); err != nil {
		t.Fatal(err)
	}
	agents := application.NewAgentService(application.AgentServiceConfig{Store: store, Tokens: tokens})
	lease := application.NewLeaseService(application.LeaseServiceConfig{Store: store, Tokens: tokens})
	return NewServer(ServerConfig{Agents: agents, Lease: lease}), credential
}
