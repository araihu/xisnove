package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/araihu/xisnove/application"
	xisclock "github.com/araihu/xisnove/internal/adapters/clock"
	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	"github.com/araihu/xisnove/internal/adapters/httpapi"
	"github.com/araihu/xisnove/internal/adapters/ids"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
	"github.com/araihu/xisnove/sdk"
)

func TestFirstObservationOpensIncidentAfterThirdFailure(t *testing.T) {
	ctx := context.Background()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "integration.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(db)
	tokens := xiscrypto.NewProductionTokenIssuer()
	passwords := xiscrypto.NewProductionPasswordHasher()
	auth := application.NewAuthService(application.AuthServiceConfig{
		Store: store, Passwords: passwords, Tokens: tokens,
		SessionDuration: time.Hour, Now: xisclock.Now, NewID: ids.NewUUID,
	})
	const password = "correct horse battery staple"
	if err := auth.BootstrapAdmin(ctx, "admin@example.com", password); err != nil {
		t.Fatal(err)
	}
	agents := application.NewAgentService(application.AgentServiceConfig{
		Store: store, Tokens: tokens, Now: xisclock.Now, NewID: ids.NewUUID,
	})
	scheduler := application.NewScheduler(store, ids.NewUUID)
	apiServer := httpapi.NewServer(httpapi.ServerConfig{
		Auth: auth,
		Configuration: application.NewConfigurationService(
			store, func() time.Time { return time.Now().UTC().Add(-time.Minute) }, ids.NewUUID,
		),
		Agents: agents,
		Lease: application.NewLeaseService(application.LeaseServiceConfig{
			Store: store, Tokens: tokens, LeaseDuration: time.Minute,
		}),
		Results: application.NewResultService(application.ResultServiceConfig{
			Store: store, Tokens: tokens, Now: xisclock.Now, NewID: ids.NewUUID,
		}),
		Health: application.NewHealthService(store),
	})
	handler, err := httpapi.NewHandler(httpapi.HandlerConfig{
		Server: apiServer,
		Ready: func(ctx context.Context) error {
			return sqlitestore.Ready(ctx, db)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := sdk.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	sessionResponse, err := client.CreateSessionWithResponse(
		ctx,
		sdk.CreateSessionRequest{
			Email:    openapi_types.Email("admin@example.com"),
			Password: pointer(password),
		},
	)
	if err != nil || sessionResponse.JSON201 == nil {
		t.Fatalf("session: response=%#v error=%v", sessionResponse, err)
	}
	adminAuth := bearer(sessionResponse.JSON201.Token)
	locationResponse, err := client.CreateLocationWithResponse(
		ctx, nil, sdk.CreateLocationRequest{Name: "public"}, adminAuth,
	)
	if err != nil || locationResponse.JSON201 == nil {
		t.Fatalf("location: response=%#v error=%v", locationResponse, err)
	}

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "not ready")
	}))
	t.Cleanup(target.Close)
	var httpProbe sdk.ProbeDefinition
	if err := httpProbe.FromHTTPProbeDefinition(sdk.HTTPProbeDefinition{
		Method: sdk.GET, Url: target.URL,
		Headers: map[string]string{},
		Body:    []byte{},
		ExpectedStatus: []sdk.StatusRange{{
			Minimum: 200, Maximum: 200,
		}},
		BodyContains:       []string{"ready"},
		BodyDoesNotContain: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	monitorResponse, err := client.CreateMonitorWithResponse(
		ctx,
		nil,
		sdk.CreateMonitorRequest{
			Name: "target", LocationId: locationResponse.JSON201.Id,
			RequiredLocation: true, IntervalSeconds: 60, TimeoutMillis: 5000,
			FailureThreshold: 3, RecoveryThreshold: 2,
			Probe: httpProbe,
		},
		adminAuth,
	)
	if err != nil || monitorResponse.JSON201 == nil {
		t.Fatalf("monitor: response=%#v error=%v", monitorResponse, err)
	}
	monitorID := monitorResponse.JSON201.Id
	enrollmentResponse, err := client.CreateAgentEnrollmentTokenWithResponse(
		ctx,
		nil,
		sdk.CreateAgentEnrollmentTokenRequest{
			LocationId: locationResponse.JSON201.Id, ExpiresInSeconds: 300,
		},
		adminAuth,
	)
	if err != nil || enrollmentResponse.JSON201 == nil {
		t.Fatalf("enrollment: response=%#v error=%v", enrollmentResponse, err)
	}
	enrolledResponse, err := client.EnrollAgentWithResponse(
		ctx,
		sdk.EnrollAgentRequest{
			Token:        pointer(enrollmentResponse.JSON201.Token),
			Name:         "integration-agent",
			Capabilities: []sdk.AgentCapability{sdk.AgentCapabilityHttp},
		},
	)
	if err != nil || enrolledResponse.JSON201 == nil {
		t.Fatalf("agent: response=%#v error=%v", enrolledResponse, err)
	}
	agentAuth := bearer(enrolledResponse.JSON201.Credential)

	var last sdk.ProbeResultInput
	for index := 0; index < 3; index++ {
		if _, err := db.ExecContext(
			ctx,
			"UPDATE monitors SET next_run_at = ? WHERE id = ?",
			time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano),
			monitorID.String(),
		); err != nil {
			t.Fatal(err)
		}
		if count, err := scheduler.EnqueueDue(ctx, 1); err != nil || count != 1 {
			t.Fatalf("enqueue: count=%d error=%v", count, err)
		}
		lease, err := client.LeaseAgentWorkWithResponse(
			ctx,
			sdk.LeaseWorkRequest{
				WaitSeconds:  0,
				Capabilities: []sdk.AgentCapability{sdk.AgentCapabilityHttp},
			},
			agentAuth,
		)
		if err != nil || lease.JSON200 == nil {
			t.Fatalf("lease: response=%#v error=%v", lease, err)
		}
		startedAt := time.Now().UTC()
		last = sdk.ProbeResultInput{
			ResultId: uuid.New(), RunId: lease.JSON200.RunId,
			LeaseToken: lease.JSON200.LeaseToken,
			StartedAt:  startedAt, FinishedAt: startedAt.Add(time.Millisecond),
			Outcome: sdk.Failed, LatencyMillis: 1, ObservedStatus: 503,
			BodyAssertionPassed: false, ErrorCode: sdk.StatusMismatch,
			DiagnosticSample: "HTTP 503",
		}
		uploaded, err := client.UploadProbeResultsWithResponse(
			ctx, sdk.ProbeResultBatch{Results: []sdk.ProbeResultInput{last}}, agentAuth,
		)
		if err != nil || uploaded.JSON200 == nil ||
			uploaded.JSON200.Acknowledgements[0].Status != sdk.Accepted {
			t.Fatalf("upload: response=%#v error=%v", uploaded, err)
		}
	}

	health, err := client.GetMonitorHealthWithResponse(ctx, monitorID, adminAuth)
	if err != nil || health.JSON200 == nil || health.JSON200.State != sdk.Down {
		t.Fatalf("health: response=%#v error=%v", health, err)
	}
	incident, err := client.GetActiveMonitorIncidentWithResponse(ctx, monitorID, adminAuth)
	if err != nil || incident.JSON200 == nil || incident.JSON200.Severity != sdk.Critical {
		t.Fatalf("incident: response=%#v error=%v", incident, err)
	}
	duplicate, err := client.UploadProbeResultsWithResponse(
		ctx, sdk.ProbeResultBatch{Results: []sdk.ProbeResultInput{last}}, agentAuth,
	)
	if err != nil || duplicate.JSON200 == nil ||
		duplicate.JSON200.Acknowledgements[0].Status != sdk.Duplicate {
		t.Fatalf("duplicate: response=%#v error=%v", duplicate, err)
	}
	var eventCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM incident_events").Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("incident events = %d", eventCount)
	}
}

func bearer(token string) sdk.RequestEditorFn {
	return func(_ context.Context, request *http.Request) error {
		request.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
}

func pointer[T any](value T) *T {
	return &value
}
