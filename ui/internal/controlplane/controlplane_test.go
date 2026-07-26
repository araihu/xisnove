package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/araihu/xisnove/sdk"
	"github.com/google/uuid"
)

func TestSDKClientUsesGeneratedOperationsAndBearerEditor(t *testing.T) {
	monitorID := uuid.New()
	monitor := sdk.Monitor{Id: monitorID, Name: "Home router", Description: "WAN edge", Kind: sdk.MonitorKindHttp, Enabled: true, CreatedAt: time.Now(), UpdatedAt: time.Now(), Labels: map[string]string{}}
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sessions":
			var request sdk.CreateSessionRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if string(request.Email) != "admin@example.test" || request.Password == nil || *request.Password != "secret" {
				t.Errorf("session request = %#v", request)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(sdk.Session{Token: "bearer-token", ExpiresAt: time.Now().Add(time.Hour)})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/sessions/current":
			assertBearer(t, r)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/status-page":
			_ = json.NewEncoder(w).Encode(sdk.PublicStatusPage{State: sdk.Up, GeneratedAt: time.Now()})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/monitors":
			assertBearer(t, r)
			if r.URL.Query().Get("limit") != "12" || r.URL.Query().Get("cursor") != "opaque/cursor" {
				t.Errorf("monitor query = %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(sdk.MonitorPage{Items: []sdk.Monitor{monitor}, Page: sdk.PageMetadata{}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/monitors/"+monitorID.String()+"/health":
			assertBearer(t, r)
			_ = json.NewEncoder(w).Encode(sdk.MonitorHealth{MonitorId: monitorID, State: sdk.Degraded, LastTransitionAt: time.Now()})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewSDKClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	token, err := client.ExchangeAdministratorCredentials(t.Context(), "admin@example.test", "secret")
	if err != nil || token != "bearer-token" {
		t.Fatalf("exchange = %q, %v", token, err)
	}
	if _, err := client.GetPublicStatusPage(t.Context()); err != nil {
		t.Fatal(err)
	}
	page, err := client.ListMonitors(t.Context(), token, "opaque/cursor", 12)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("page = %#v, %v", page, err)
	}
	health, err := client.GetMonitorHealth(t.Context(), token, monitorID)
	if err != nil || health.State != sdk.Degraded {
		t.Fatalf("health = %#v, %v", health, err)
	}
	if err := client.RevokeSession(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 5 {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestSDKClientMapsSessionUnauthorizedWithoutRetainingBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"about:blank","title":"denied","status":401,"code":"invalid_credentials","correlationId":"request-1","detail":"secret diagnostic"}`))
	}))
	defer server.Close()
	client, err := NewSDKClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ExchangeAdministratorCredentials(t.Context(), "admin@example.test", "wrong")
	if !errors.Is(err, ErrInvalidCredentials) || strings.Contains(err.Error(), "secret diagnostic") {
		t.Fatalf("error = %v", err)
	}
}

func assertBearer(t *testing.T, request *http.Request) {
	t.Helper()
	if request.Header.Get("Authorization") != "Bearer bearer-token" {
		t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
	}
}

func TestFakeExchangesOnlyConfiguredAdministratorCredentials(t *testing.T) {
	client := NewFake("local-admin", "correct horse", "opaque-control-plane-session")

	credential, err := client.ExchangeAdministratorCredentials(t.Context(), "local-admin", "correct horse")
	if err != nil {
		t.Fatalf("exchange valid credentials: %v", err)
	}
	if credential != "opaque-control-plane-session" {
		t.Fatalf("credential = %q, want opaque fixture", credential)
	}

	credential, err = client.ExchangeAdministratorCredentials(t.Context(), "local-admin", "wrong")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("invalid credentials error = %v, want ErrInvalidCredentials", err)
	}
	if credential != "" {
		t.Fatalf("invalid credentials leaked credential %q", credential)
	}
}

func TestFakeHonorsCancellationWithoutExaminingCredentials(t *testing.T) {
	client := NewFake("local-admin", "correct horse", "opaque-control-plane-session")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	credential, err := client.ExchangeAdministratorCredentials(ctx, "local-admin", "correct horse")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("exchange error = %v, want context cancellation", err)
	}
	if credential != "" {
		t.Fatalf("canceled exchange returned credential %q", credential)
	}
}

func TestFakeRevokesOnlyItsIssuedCredential(t *testing.T) {
	client := NewFake("local-admin", "correct horse", "opaque-control-plane-session")

	if err := client.RevokeSession(t.Context(), "opaque-control-plane-session"); err != nil {
		t.Fatalf("revoke issued credential: %v", err)
	}
	if err := client.RevokeSession(t.Context(), "different-session"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoke unknown credential error = %v, want ErrUnauthorized", err)
	}
}
