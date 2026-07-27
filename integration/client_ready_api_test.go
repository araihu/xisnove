package integration_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"reflect"
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

func TestClientReadyAPI(t *testing.T) {
	ctx := context.Background()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "client-ready.db"))
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
		Store: store, Passwords: xiscrypto.NewProductionPasswordHasher(), Tokens: tokens,
		SessionDuration: time.Hour, Now: xisclock.Now, NewID: ids.NewUUID,
	})
	const password = "correct horse battery staple"
	if err := auth.BootstrapAdmin(ctx, "admin@example.com", password); err != nil {
		t.Fatal(err)
	}
	agents := application.NewAgentService(application.AgentServiceConfig{
		Store: store, Tokens: tokens, Now: xisclock.Now, NewID: ids.NewUUID,
	})
	cursors, err := application.NewHMACCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	apiTokens := application.NewAPITokenService(application.APITokenServiceConfig{
		Store: store, Tokens: tokens, Now: xisclock.Now, NewID: ids.NewUUID,
	})
	configuration := application.NewConfigurationService(store, xisclock.Now, ids.NewUUID)
	management := application.NewManagementService(application.ManagementServiceConfig{
		Store: store, Cursors: cursors, Tokens: tokens, NewID: ids.NewUUID,
	})
	scheduler := application.NewScheduler(store, ids.NewUUID)
	handler, err := httpapi.NewHandler(httpapi.HandlerConfig{
		Server: httpapi.NewServer(httpapi.ServerConfig{
			Auth: auth, APITokens: apiTokens, Configuration: configuration,
			Agents: agents, Management: management,
			Lease: application.NewLeaseService(application.LeaseServiceConfig{
				Store: store, Tokens: tokens, LeaseDuration: time.Minute,
			}),
			Results: application.NewResultService(application.ResultServiceConfig{
				Store: store, Tokens: tokens, Now: xisclock.Now, NewID: ids.NewUUID,
			}),
			Health: application.NewHealthService(store),
		}),
		Ready: func(ctx context.Context) error { return sqlitestore.Ready(ctx, db) },
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

	session, err := client.CreateSessionWithResponse(ctx, sdk.CreateSessionRequest{
		Email: openapi_types.Email("admin@example.com"), Password: pointer(password),
	})
	if err != nil || session.JSON201 == nil {
		t.Fatalf("create session response=%#v err=%v", session, err)
	}
	adminAuth := bearer(session.JSON201.Token)
	idempotency := sdk.IdempotencyKey("client-token")
	createdToken, err := client.CreateAPITokenWithResponse(ctx,
		&sdk.CreateAPITokenParams{IdempotencyKey: &idempotency},
		sdk.CreateAPITokenRequest{Name: "client-ready", Scopes: []sdk.APITokenScope{
			sdk.LocationsRead, sdk.LocationsWrite, sdk.MonitorsRead, sdk.MonitorsWrite,
			sdk.AgentsRead, sdk.AgentsWrite, sdk.IncidentsRead,
		}}, adminAuth,
	)
	if err != nil || createdToken.JSON201 == nil || createdToken.JSON201.Token == "" {
		t.Fatalf("create API token response=%#v err=%v", createdToken, err)
	}
	clientAuth := bearer(createdToken.JSON201.Token)

	locations := make([]sdk.Location, 0, 2)
	for index, name := range []string{"edge", "public"} {
		key := sdk.IdempotencyKey("location-" + name)
		response, err := client.CreateLocationWithResponse(ctx,
			&sdk.CreateLocationParams{IdempotencyKey: &key}, sdk.CreateLocationRequest{Name: name}, clientAuth,
		)
		if err != nil || response.JSON201 == nil {
			t.Fatalf("create location %d response=%#v err=%v", index, response, err)
		}
		locations = append(locations, *response.JSON201)
	}
	limit := sdk.Limit(1)
	firstPage, err := client.ListLocationsWithResponse(ctx, &sdk.ListLocationsParams{Limit: &limit}, clientAuth)
	if err != nil || firstPage.JSON200 == nil || len(firstPage.JSON200.Items) != 1 || firstPage.JSON200.Page.NextCursor == nil {
		t.Fatalf("first location page response=%#v err=%v", firstPage, err)
	}
	cursor := sdk.Cursor(*firstPage.JSON200.Page.NextCursor)
	secondPage, err := client.ListLocationsWithResponse(ctx, &sdk.ListLocationsParams{Limit: &limit, Cursor: &cursor}, clientAuth)
	if err != nil || secondPage.JSON200 == nil || len(secondPage.JSON200.Items) != 1 {
		t.Fatalf("second location page response=%#v err=%v", secondPage, err)
	}
	if firstPage.JSON200.Items[0].Name != "edge" || secondPage.JSON200.Items[0].Name != "public" ||
		firstPage.JSON200.Items[0].Id == secondPage.JSON200.Items[0].Id || secondPage.JSON200.Page.NextCursor != nil {
		t.Fatalf("location pages did not form one ordered terminal traversal: first=%#v second=%#v", firstPage.JSON200, secondPage.JSON200)
	}

	probe := clientReadyHTTPProbe(t)
	createMonitorKey := sdk.IdempotencyKey("create-monitor")
	createdMonitor, err := client.CreateMonitorWithResponse(ctx,
		&sdk.CreateMonitorParams{IdempotencyKey: &createMonitorKey},
		sdk.CreateMonitorRequest{
			Name: "home-lab", LocationId: locations[0].Id, RequiredLocation: true,
			IntervalSeconds: 60, TimeoutMillis: 5000, FailureThreshold: 2, RecoveryThreshold: 2,
			Probe: probe,
		}, clientAuth,
	)
	if err != nil || createdMonitor.JSON201 == nil {
		t.Fatalf("create monitor response=%#v err=%v", createdMonitor, err)
	}
	updateMonitorKey := sdk.IdempotencyKey("update-monitor")
	updateBody := sdk.UpdateMonitorRequest{
		Name: "home-lab external", Description: "public edge", Labels: map[string]string{"site": "home"},
		DisplayOrder: 4, Public: true, Enabled: true,
		IntervalSeconds: 30, TimeoutMillis: 3000, FailureThreshold: 1, RecoveryThreshold: 2,
		LocationId: locations[0].Id, RequiredLocation: true, Probe: probe,
	}
	updatedMonitor, err := client.UpdateMonitorWithResponse(ctx, createdMonitor.JSON201.Id,
		&sdk.UpdateMonitorParams{IdempotencyKey: &updateMonitorKey}, updateBody, clientAuth,
	)
	if err != nil || updatedMonitor.JSON200 == nil || !monitorMatchesReplacement(*updatedMonitor.JSON200, updateBody) {
		t.Fatalf("update monitor response=%#v err=%v", updatedMonitor, err)
	}
	replayMonitor, err := client.UpdateMonitorWithResponse(ctx, createdMonitor.JSON201.Id,
		&sdk.UpdateMonitorParams{IdempotencyKey: &updateMonitorKey}, updateBody, clientAuth,
	)
	if err != nil || replayMonitor.JSON200 == nil ||
		!monitorMatchesReplacement(*replayMonitor.JSON200, updateBody) ||
		!replayMonitor.JSON200.UpdatedAt.Equal(updatedMonitor.JSON200.UpdatedAt) {
		t.Fatalf("replay monitor response=%#v err=%v", replayMonitor, err)
	}
	schedulerContext, stopScheduler := context.WithCancel(ctx)
	t.Cleanup(stopScheduler)
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-schedulerContext.Done():
				return
			case <-ticker.C:
				_, _ = scheduler.EnqueueDue(schedulerContext, 10)
			}
		}
	}()
	enrollment, err := client.CreateAgentEnrollmentTokenWithResponse(ctx, nil,
		sdk.CreateAgentEnrollmentTokenRequest{LocationId: locations[0].Id, ExpiresInSeconds: 300}, adminAuth,
	)
	if err != nil || enrollment.JSON201 == nil {
		t.Fatalf("create enrollment response=%#v err=%v", enrollment, err)
	}
	credential := "client-ready-agent-credential-0000000000000001"
	enrolled, err := client.EnrollAgentWithResponse(ctx, &sdk.EnrollAgentParams{
		IdempotencyKey: sdk.RequiredIdempotencyKey("client-ready-agent-enrollment"),
	}, sdk.EnrollAgentRequest{
		Token: pointer(enrollment.JSON201.Token), Name: "cluster-agent",
		Credential:   &credential,
		Capabilities: []sdk.AgentCapability{sdk.AgentCapabilityHttp, sdk.AgentCapabilityKubernetesDiscovery},
	})
	if err != nil || enrolled.JSON201 == nil {
		t.Fatalf("enroll response=%#v err=%v", enrolled, err)
	}
	agentID := enrolled.JSON201.AgentId
	oldAgentAuth := bearer(enrolled.JSON201.Credential)
	var lease *sdk.LeaseAgentWorkResponse
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		lease, err = client.LeaseAgentWorkWithResponse(ctx, sdk.LeaseWorkRequest{
			WaitSeconds: 0, Capabilities: []sdk.AgentCapability{sdk.AgentCapabilityHttp},
		}, oldAgentAuth)
		if err != nil {
			t.Fatal(err)
		}
		if lease.JSON200 != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if lease == nil || lease.JSON200 == nil {
		t.Fatalf("lease response=%#v", lease)
	}
	startedAt := time.Now().UTC()
	result := sdk.ProbeResultInput{
		ResultId: uuid.New(), RunId: lease.JSON200.RunId, LeaseToken: lease.JSON200.LeaseToken,
		StartedAt: startedAt, FinishedAt: startedAt.Add(time.Millisecond),
		Outcome: sdk.Failed, LatencyMillis: 1, ObservedStatus: 503,
		BodyAssertionPassed: false, ErrorCode: sdk.StatusMismatch, DiagnosticSample: "HTTP 503",
	}
	uploaded, err := client.UploadProbeResultsWithResponse(ctx,
		sdk.ProbeResultBatch{Results: []sdk.ProbeResultInput{result}}, oldAgentAuth,
	)
	if err != nil || uploaded.JSON200 == nil || len(uploaded.JSON200.Acknowledgements) != 1 ||
		uploaded.JSON200.Acknowledgements[0].Status != sdk.Accepted {
		t.Fatalf("upload result response=%#v err=%v", uploaded, err)
	}
	incidents, err := client.ListIncidentsWithResponse(ctx, nil, clientAuth)
	if err != nil || incidents.JSON200 == nil || len(incidents.JSON200.Items) != 1 {
		t.Fatalf("list incidents response=%#v err=%v", incidents, err)
	}
	events, err := client.ListIncidentEventsWithResponse(ctx, incidents.JSON200.Items[0].Id, nil, clientAuth)
	if err != nil || events.JSON200 == nil || len(events.JSON200.Items) != 1 {
		t.Fatalf("list incident events response=%#v err=%v", events, err)
	}
	if disabled, err := client.DisableMonitorWithResponse(ctx, createdMonitor.JSON201.Id, clientAuth); err != nil || disabled.StatusCode() != 204 {
		t.Fatalf("disable monitor response=%#v err=%v", disabled, err)
	}
	disabledMonitor, err := client.GetMonitorWithResponse(ctx, createdMonitor.JSON201.Id, clientAuth)
	if err != nil || disabledMonitor.JSON200 == nil || disabledMonitor.JSON200.Enabled {
		t.Fatalf("disabled monitor response=%#v err=%v", disabledMonitor, err)
	}
	time.Sleep(30 * time.Millisecond)
	if noWork, err := client.LeaseAgentWorkWithResponse(ctx, sdk.LeaseWorkRequest{
		WaitSeconds: 0, Capabilities: []sdk.AgentCapability{sdk.AgentCapabilityHttp},
	}, oldAgentAuth); err != nil || noWork.StatusCode() != 204 {
		t.Fatalf("disabled monitor scheduled work response=%#v err=%v", noWork, err)
	}
	agentsPage, err := client.ListAgentsWithResponse(ctx, nil, clientAuth)
	if err != nil || agentsPage.JSON200 == nil || len(agentsPage.JSON200.Items) != 1 {
		t.Fatalf("list agents response=%#v err=%v", agentsPage, err)
	}
	newCapabilities := []sdk.AgentCapability{
		sdk.AgentCapabilityHttp, sdk.AgentCapabilityKubernetesDiscovery, sdk.AgentCapabilityKubernetesWatch,
	}
	updateAgentKey := sdk.IdempotencyKey("update-agent")
	updatedAgent, err := client.UpdateAgentWithResponse(ctx, agentID,
		&sdk.UpdateAgentParams{IdempotencyKey: &updateAgentKey},
		sdk.UpdateAgentRequest{Capabilities: &newCapabilities}, clientAuth,
	)
	if err != nil || updatedAgent.JSON200 == nil || len(updatedAgent.JSON200.Capabilities) != 3 {
		t.Fatalf("update agent response=%#v err=%v", updatedAgent, err)
	}
	rotationKey := sdk.IdempotencyKey("rotate-agent")
	rotated, err := client.RotateAgentCredentialWithResponse(ctx, agentID,
		&sdk.RotateAgentCredentialParams{IdempotencyKey: &rotationKey}, clientAuth,
	)
	if err != nil || rotated.JSON201 == nil || rotated.JSON201.Credential == nil {
		t.Fatalf("rotate response=%#v err=%v", rotated, err)
	}
	newAgentAuth := bearer(*rotated.JSON201.Credential)
	if response, err := client.HeartbeatAgentWithResponse(ctx,
		sdk.AgentHeartbeat{CredentialGeneration: 1, Version: "v1-overlap", Capabilities: newCapabilities}, oldAgentAuth,
	); err != nil || response.StatusCode() != 204 {
		t.Fatalf("old generation overlap heartbeat response=%#v err=%v", response, err)
	}
	heartbeat := sdk.AgentHeartbeat{
		CredentialGeneration: 2, Version: "v2", Capabilities: newCapabilities,
	}
	if response, err := client.HeartbeatAgentWithResponse(ctx, heartbeat, newAgentAuth); err != nil || response.StatusCode() != 204 {
		t.Fatalf("replacement heartbeat response=%#v err=%v", response, err)
	}
	if response, err := client.RevokeAgentCredentialGenerationWithResponse(ctx, agentID, 1, clientAuth); err != nil || response.StatusCode() != 204 {
		t.Fatalf("revoke old generation response=%#v err=%v", response, err)
	}
	if response, err := client.HeartbeatAgentWithResponse(ctx,
		sdk.AgentHeartbeat{CredentialGeneration: 1, Version: "v1", Capabilities: newCapabilities}, oldAgentAuth,
	); err != nil || response.StatusCode() != 401 {
		t.Fatalf("revoked generation heartbeat response=%#v err=%v", response, err)
	}
	if response, err := client.HeartbeatAgentWithResponse(ctx, heartbeat, newAgentAuth); err != nil || response.StatusCode() != 204 {
		t.Fatalf("replacement heartbeat after old revoke response=%#v err=%v", response, err)
	}

	if response, err := client.RevokeAPITokenWithResponse(ctx, createdToken.JSON201.ApiToken.Id, adminAuth); err != nil || response.StatusCode() != 204 {
		t.Fatalf("revoke API token response=%#v err=%v", response, err)
	}
	if response, err := client.ListLocationsWithResponse(ctx, nil, clientAuth); err != nil || response.StatusCode() != 401 {
		t.Fatalf("revoked API token response=%#v err=%v", response, err)
	}
}

func monitorMatchesReplacement(monitor sdk.Monitor, request sdk.UpdateMonitorRequest) bool {
	return monitor.Name == request.Name &&
		monitor.Description == request.Description &&
		reflect.DeepEqual(monitor.Labels, request.Labels) &&
		monitor.DisplayOrder == request.DisplayOrder &&
		monitor.Public == request.Public && monitor.Enabled == request.Enabled &&
		monitor.IntervalSeconds == request.IntervalSeconds && monitor.TimeoutMillis == request.TimeoutMillis &&
		monitor.FailureThreshold == request.FailureThreshold && monitor.RecoveryThreshold == request.RecoveryThreshold &&
		monitor.LocationId == request.LocationId && monitor.RequiredLocation == request.RequiredLocation &&
		reflect.DeepEqual(monitor.Probe, request.Probe)
}

func clientReadyHTTPProbe(t *testing.T) sdk.ProbeDefinition {
	t.Helper()
	var probe sdk.ProbeDefinition
	if err := probe.FromHTTPProbeDefinition(sdk.HTTPProbeDefinition{
		Kind: sdk.HTTPProbeDefinitionKindHttp, Method: sdk.GET,
		Url: "https://example.test/health", Headers: map[string]string{}, Body: []byte{},
		ExpectedStatus: []sdk.StatusRange{{Minimum: 200, Maximum: 299}},
		BodyContains:   []string{}, BodyDoesNotContain: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	return probe
}
