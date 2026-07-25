package integration_test

import (
	"context"
	"encoding/json"
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

func TestProtocolBreadthControlPlanePath(t *testing.T) {
	ctx := context.Background()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "protocols.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(db)
	tokens := xiscrypto.NewProductionTokenIssuer()
	auth := application.NewAuthService(application.AuthServiceConfig{
		Store: store, Passwords: xiscrypto.NewProductionPasswordHasher(),
		Tokens: tokens, SessionDuration: time.Hour, Now: xisclock.Now, NewID: ids.NewUUID,
	})
	const password = "correct horse battery staple"
	if err := auth.BootstrapAdmin(ctx, "admin@example.com", password); err != nil {
		t.Fatal(err)
	}
	const leaseDuration = 45 * time.Second
	server := httpapi.NewServer(httpapi.ServerConfig{
		Auth: auth,
		Configuration: application.NewConfigurationService(
			store, func() time.Time { return time.Now().UTC().Add(-time.Minute) }, ids.NewUUID,
		),
		Agents: application.NewAgentService(application.AgentServiceConfig{
			Store: store, Tokens: tokens, Now: xisclock.Now, NewID: ids.NewUUID,
		}),
		Lease: application.NewLeaseService(application.LeaseServiceConfig{
			Store: store, Tokens: tokens, LeaseDuration: leaseDuration,
		}),
		Results: application.NewResultService(application.ResultServiceConfig{
			Store: store, Tokens: tokens, Now: xisclock.Now, NewID: ids.NewUUID,
			LeaseDuration: leaseDuration,
		}),
		Health: application.NewHealthService(store),
	})
	handler, err := httpapi.NewHandler(httpapi.HandlerConfig{
		Server: server,
		Ready:  func(ctx context.Context) error { return sqlitestore.Ready(ctx, db) },
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	client, err := sdk.NewClientWithResponses(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.CreateSessionWithResponse(ctx, sdk.CreateSessionRequest{
		Email: openapi_types.Email("admin@example.com"), Password: pointer(password),
	})
	if err != nil || session.JSON201 == nil {
		t.Fatalf("session=%#v error=%v", session, err)
	}
	admin := bearer(session.JSON201.Token)
	location, err := client.CreateLocationWithResponse(
		ctx, sdk.CreateLocationRequest{Name: "hybrid-lab"}, admin,
	)
	if err != nil || location.JSON201 == nil {
		t.Fatalf("location=%#v error=%v", location, err)
	}

	monitorIDs := make([]string, 0, 3)
	for _, request := range []sdk.CreateMonitorRequest{
		monitorRequest(t, "web", location.JSON201.Id, httpDefinition(t)),
		monitorRequest(t, "postgres", location.JSON201.Id, tcpDefinition(t)),
		monitorRequest(t, "cluster-dns", location.JSON201.Id, dnsDefinition(t)),
	} {
		response, err := client.CreateMonitorWithResponse(ctx, request, admin)
		if err != nil || response.JSON201 == nil {
			t.Fatalf("monitor=%#v error=%v", response, err)
		}
		monitorIDs = append(monitorIDs, response.JSON201.Id.String())
	}
	enrollment, err := client.CreateAgentEnrollmentTokenWithResponse(
		ctx,
		sdk.CreateAgentEnrollmentTokenRequest{
			LocationId: location.JSON201.Id, ExpiresInSeconds: 300,
		},
		admin,
	)
	if err != nil || enrollment.JSON201 == nil {
		t.Fatalf("enrollment=%#v error=%v", enrollment, err)
	}
	enrolled, err := client.EnrollAgentWithResponse(ctx, sdk.EnrollAgentRequest{
		Token: pointer(enrollment.JSON201.Token), Name: "hybrid-agent",
		Capabilities: []sdk.AgentCapability{
			sdk.AgentCapabilityHttp, sdk.AgentCapabilityTcp, sdk.AgentCapabilityDns,
		},
	})
	if err != nil || enrolled.JSON201 == nil {
		t.Fatalf("agent=%#v error=%v", enrolled, err)
	}
	agent := bearer(enrolled.JSON201.Credential)
	scheduler := application.NewScheduler(store, ids.NewUUID)
	if inserted, err := scheduler.EnqueueDue(ctx, 10); err != nil || inserted != 3 {
		t.Fatalf("inserted=%d error=%v", inserted, err)
	}

	results := make([]sdk.ProbeResultInput, 0, 3)
	kinds := map[string]bool{}
	for index := 0; index < 3; index++ {
		lease, err := client.LeaseAgentWorkWithResponse(ctx, sdk.LeaseWorkRequest{
			Capabilities: []sdk.AgentCapability{
				sdk.AgentCapabilityHttp, sdk.AgentCapabilityTcp, sdk.AgentCapabilityDns,
			},
		}, agent)
		if err != nil || lease.JSON200 == nil {
			t.Fatalf("lease=%#v error=%v", lease, err)
		}
		kind, _ := lease.JSON200.Probe.Discriminator()
		kinds[kind] = true
		now := time.Now().UTC()
		observed := []string{"observed-" + kind}
		zero := int64(0)
		results = append(results, sdk.ProbeResultInput{
			ResultId: idsUUID(t), RunId: lease.JSON200.RunId,
			LeaseToken: lease.JSON200.LeaseToken,
			StartedAt:  now, FinishedAt: now, Outcome: sdk.Passed,
			LatencyMillis: 0, ObservedStatus: 200, BodyAssertionPassed: true,
			ErrorCode: sdk.Empty, DiagnosticSample: "",
			ObservedValues: &observed,
			ProtocolTimings: &sdk.ProtocolTimings{
				DnsMillis: &zero, ConnectMillis: &zero,
			},
		})
	}
	if len(kinds) != 3 || !kinds["http"] || !kinds["tcp"] || !kinds["dns"] {
		t.Fatalf("leased kinds = %#v", kinds)
	}
	uploaded, err := client.UploadProbeResultsWithResponse(
		ctx, sdk.ProbeResultBatch{Results: results}, agent,
	)
	if err != nil || uploaded.JSON200 == nil ||
		len(uploaded.JSON200.Acknowledgements) != 3 {
		t.Fatalf("upload=%#v error=%v", uploaded, err)
	}
	for _, monitorID := range monitorIDs {
		health, err := client.GetMonitorHealthWithResponse(
			ctx, mustUUID(t, monitorID), admin,
		)
		if err != nil || health.JSON200 == nil || health.JSON200.State != sdk.Up {
			t.Fatalf("health=%#v error=%v", health, err)
		}
	}
	var persisted []byte
	if err := db.QueryRow(
		"SELECT observed_values_json FROM probe_results ORDER BY id LIMIT 1",
	).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	var values []string
	if err := json.Unmarshal(persisted, &values); err != nil || len(values) != 1 {
		t.Fatalf("observed=%s error=%v", persisted, err)
	}

	databaseNow, err := store.Repositories().Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lagged := databaseNow.Add(-99 * time.Minute).Format(time.RFC3339Nano)
	if _, err := db.Exec("UPDATE monitors SET next_run_at = ? WHERE id = ?", lagged, monitorIDs[0]); err != nil {
		t.Fatal(err)
	}
	stats, err := scheduler.EnqueueDueWithStats(ctx, 1)
	if err != nil || stats.Inserted != 1 || stats.SkippedIntervals != 99 {
		t.Fatalf("stats=%#v error=%v", stats, err)
	}

	if _, err := db.Exec(
		"UPDATE location_health SET stale_at = ? WHERE monitor_id = ?",
		databaseNow.Add(-time.Second).Format(time.RFC3339Nano), monitorIDs[0],
	); err != nil {
		t.Fatal(err)
	}
	staleness := application.NewStalenessService(store, ids.NewUUID)
	if marked, err := staleness.MarkDue(ctx, 10); err != nil || marked != 1 {
		t.Fatalf("marked=%d error=%v", marked, err)
	}
	incident, err := client.GetActiveMonitorIncidentWithResponse(
		ctx, mustUUID(t, monitorIDs[0]), admin,
	)
	if err != nil || incident.JSON200 == nil || incident.JSON200.Severity != sdk.Warning {
		t.Fatalf("incident=%#v error=%v", incident, err)
	}
}

func monitorRequest(
	t *testing.T,
	name string,
	locationID openapi_types.UUID,
	probe sdk.ProbeDefinition,
) sdk.CreateMonitorRequest {
	t.Helper()
	return sdk.CreateMonitorRequest{
		Name: name, LocationId: locationID, RequiredLocation: true,
		IntervalSeconds: 60, TimeoutMillis: 5000,
		FailureThreshold: 1, RecoveryThreshold: 1, Probe: probe,
	}
}

func httpDefinition(t *testing.T) sdk.ProbeDefinition {
	t.Helper()
	var probe sdk.ProbeDefinition
	if err := probe.FromHTTPProbeDefinition(sdk.HTTPProbeDefinition{
		Method: sdk.GET, Url: "https://example.com/health",
		Headers: map[string]string{}, Body: []byte{},
		ExpectedStatus: []sdk.StatusRange{{Minimum: 200, Maximum: 299}},
		BodyContains:   []string{}, BodyDoesNotContain: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	return probe
}

func tcpDefinition(t *testing.T) sdk.ProbeDefinition {
	t.Helper()
	var probe sdk.ProbeDefinition
	if err := probe.FromTCPProbeDefinition(sdk.TCPProbeDefinition{
		Host: "postgres.internal", Port: 5432, Send: []byte{}, Expect: []byte{},
	}); err != nil {
		t.Fatal(err)
	}
	return probe
}

func dnsDefinition(t *testing.T) sdk.ProbeDefinition {
	t.Helper()
	var probe sdk.ProbeDefinition
	if err := probe.FromDNSProbeDefinition(sdk.DNSProbeDefinition{
		Resolver: "1.1.1.1:53", Name: "example.com",
		RecordType: sdk.A, ExpectedValues: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	return probe
}

func idsUUID(t *testing.T) openapi_types.UUID {
	t.Helper()
	return mustUUID(t, ids.NewUUID())
}

func mustUUID(t *testing.T, value string) openapi_types.UUID {
	t.Helper()
	parsed, err := uuid.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
