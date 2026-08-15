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
		case r.Method == http.MethodGet && r.URL.Path == "/v1/monitors/"+monitorID.String()+"/availability/history":
			assertBearer(t, r)
			if r.URL.Query().Get("limit") != "64" {
				t.Errorf("history query = %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(sdk.MonitorAvailabilityHistory{MonitorId: monitorID, Samples: []sdk.MonitorAvailabilitySample{{Id: uuid.New(), LocationId: uuid.New(), ObservedAt: time.Now(), Outcome: sdk.MonitorAvailabilitySampleOutcomePassed}}})
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
	history, err := client.GetMonitorAvailabilityHistory(t.Context(), token, monitorID, time.Now().Add(-time.Hour), time.Now(), 64)
	if err != nil || len(history.Samples) != 1 || history.Samples[0].Outcome != sdk.MonitorAvailabilitySampleOutcomePassed {
		t.Fatalf("history = %#v, %v", history, err)
	}
	if err := client.RevokeSession(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 6 {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestSDKClientGetsBoundedMonitorStateHistoryWithBearer(t *testing.T) {
	monitorID := uuid.MustParse("10000000-0000-4000-8000-000000000099")
	startsAt := time.Date(2026, time.August, 15, 9, 0, 0, 0, time.UTC)
	endsAt := startsAt.Add(3 * time.Hour)
	stateTick := sdk.MonitorStateTick{
		Id:         uuid.MustParse("20000000-0000-4000-8000-000000000099"),
		MonitorId:  monitorID,
		Lifecycle:  sdk.Active,
		Health:     sdk.Degraded,
		ReasonCode: sdk.StateTickReasonCodeProbeFailure,
		Actor:      sdk.StateTickActor{Kind: sdk.StateTickActorKindAgent},
		OccurredAt: startsAt.Add(time.Minute),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/monitors/"+monitorID.String()+"/state-ticks" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		assertBearer(t, r)
		if got, want := r.URL.Query().Get("startsAt"), startsAt.Format(time.RFC3339); got != want {
			t.Errorf("startsAt = %q, want %q", got, want)
		}
		if got, want := r.URL.Query().Get("endsAt"), endsAt.Format(time.RFC3339); got != want {
			t.Errorf("endsAt = %q, want %q", got, want)
		}
		if got, want := r.URL.Query().Get("limit"), "10000"; got != want {
			t.Errorf("limit = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sdk.MonitorStateHistory{
			MonitorId:   monitorID,
			StartsAt:    startsAt,
			EndsAt:      endsAt,
			GeneratedAt: endsAt,
			Ticks:       []sdk.MonitorStateTick{stateTick},
		})
	}))
	defer server.Close()

	client, err := NewSDKClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	history, err := client.GetMonitorStateHistory(t.Context(), "bearer-token", monitorID, startsAt, endsAt, 10000)
	if err != nil {
		t.Fatalf("get monitor state history: %v", err)
	}
	if history.MonitorId != monitorID || len(history.Ticks) != 1 || history.Ticks[0].ReasonCode != sdk.StateTickReasonCodeProbeFailure {
		t.Fatalf("history = %#v", history)
	}
}

func TestSDKClientManagesLocationsWithBearer(t *testing.T) {
	locationID := uuid.MustParse("10000000-0000-0000-0000-000000000201")
	updatedAt := time.Now().UTC()
	enabled := true
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/locations":
			assertBearer(t, r)
			if r.URL.Query().Get("limit") != "12" || r.URL.Query().Get("cursor") != "opaque/cursor" {
				t.Errorf("location query = %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(sdk.LocationPage{Items: []sdk.Location{{Id: locationID, Name: "home-lab", Enabled: &enabled, CreatedAt: updatedAt, UpdatedAt: &updatedAt}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/locations":
			assertBearer(t, r)
			if r.Header.Get("Idempotency-Key") == "" {
				t.Error("create location missing idempotency key")
			}
			var request sdk.CreateLocationRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.Name != "edge" {
				t.Errorf("create location body = %#v", request)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(sdk.Location{Id: locationID, Name: request.Name, Protocol: sdk.LocationProtocol("http"), Policy: sdk.LocationPolicy{IntervalSeconds: 60, TimeoutMillis: 5000, FailureThreshold: 3, RecoveryThreshold: 2}, Enabled: &enabled, CreatedAt: updatedAt, UpdatedAt: &updatedAt})
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/locations/"+locationID.String():
			assertBearer(t, r)
			if r.Header.Get("Idempotency-Key") == "" {
				t.Error("update location missing idempotency key")
			}
			var request sdk.UpdateLocationRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.Name == nil || *request.Name != "edge-renamed" || request.Enabled == nil || *request.Enabled {
				t.Errorf("update location body = %#v", request)
			}
			_ = json.NewEncoder(w).Encode(sdk.Location{Id: locationID, Name: *request.Name, Enabled: request.Enabled, CreatedAt: updatedAt, UpdatedAt: &updatedAt})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/locations/"+locationID.String():
			assertBearer(t, r)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewSDKClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.ListLocations(t.Context(), "bearer-token", "opaque/cursor", 12)
	if err != nil || len(page.Items) != 1 || page.Items[0].Name != "home-lab" {
		t.Fatalf("locations page = %#v, %v", page, err)
	}
	created, err := client.CreateLocation(t.Context(), "bearer-token", LocationInput{Name: "edge"})
	if err != nil || created.Name != "edge" {
		t.Fatalf("created location = %#v, %v", created, err)
	}
	updated, err := client.UpdateLocation(t.Context(), "bearer-token", locationID, LocationInput{Name: "edge-renamed", Enabled: false})
	if err != nil || updated.Name != "edge-renamed" || updated.Enabled == nil || *updated.Enabled {
		t.Fatalf("updated location = %#v, %v", updated, err)
	}
	if err := client.DisableLocation(t.Context(), "bearer-token", locationID); err != nil {
		t.Fatalf("disable location: %v", err)
	}
	if len(calls) != 4 {
		t.Fatalf("location calls = %#v", calls)
	}
}

func TestBoundStateHistoryKeepsHalfOpenWindowAndNewestRecords(t *testing.T) {
	monitorID := uuid.MustParse("10000000-0000-4000-8000-000000000099")
	otherMonitorID := uuid.MustParse("10000000-0000-4000-8000-000000000098")
	startsAt := time.Date(2026, time.August, 15, 9, 0, 0, 0, time.UTC)
	endsAt := startsAt.Add(3 * time.Hour)
	tick := func(id uuid.UUID, monitorID uuid.UUID, occurredAt time.Time) sdk.MonitorStateTick {
		return sdk.MonitorStateTick{Id: id, MonitorId: monitorID, Lifecycle: sdk.Active, Health: sdk.Up, OccurredAt: occurredAt}
	}
	history := sdk.MonitorStateHistory{
		MonitorId: monitorID,
		StartsAt:  startsAt.Add(-time.Hour),
		EndsAt:    endsAt.Add(time.Hour),
		Ticks: []sdk.MonitorStateTick{
			tick(uuid.MustParse("20000000-0000-0000-0000-000000000004"), monitorID, startsAt.Add(2*time.Hour)),
			tick(uuid.MustParse("20000000-0000-0000-0000-000000000001"), monitorID, startsAt.Add(-time.Minute)),
			tick(uuid.MustParse("20000000-0000-0000-0000-000000000003"), monitorID, endsAt),
			tick(uuid.MustParse("20000000-0000-0000-0000-000000000002"), monitorID, startsAt.Add(time.Hour)),
			tick(uuid.MustParse("20000000-0000-0000-0000-000000000006"), monitorID, startsAt.Add(30*time.Minute)),
			tick(uuid.MustParse("20000000-0000-0000-0000-000000000005"), otherMonitorID, startsAt.Add(90*time.Minute)),
		},
	}
	bounded := BoundStateHistory(history, monitorID, startsAt, endsAt, 2)
	if !bounded.StartsAt.Equal(startsAt) || !bounded.EndsAt.Equal(endsAt) {
		t.Fatalf("window = %s..%s, want %s..%s", bounded.StartsAt, bounded.EndsAt, startsAt, endsAt)
	}
	if !bounded.Truncated || len(bounded.Ticks) != 2 {
		t.Fatalf("truncated=%v ticks=%d, want true/2", bounded.Truncated, len(bounded.Ticks))
	}
	if got, want := bounded.Ticks[0].Id.String(), "20000000-0000-0000-0000-000000000002"; got != want {
		t.Fatalf("first tick = %s, want %s", got, want)
	}
	if got, want := bounded.Ticks[1].Id.String(), "20000000-0000-0000-0000-000000000004"; got != want {
		t.Fatalf("last tick = %s, want %s", got, want)
	}
}

func TestSDKClientMapsStateHistoryUnauthorizedToSessionError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"about:blank","title":"denied","status":401,"code":"unauthorized","detail":"secret diagnostic"}`))
	}))
	defer server.Close()
	client, err := NewSDKClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GetMonitorStateHistory(t.Context(), "expired-token", uuid.New(), time.Now().Add(-time.Hour), time.Now(), 100)
	if !errors.Is(err, ErrUnauthorized) || strings.Contains(err.Error(), "secret diagnostic") {
		t.Fatalf("state history error = %v", err)
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
