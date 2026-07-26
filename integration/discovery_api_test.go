package integration_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/araihu/xisnove/application"
	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	"github.com/araihu/xisnove/internal/adapters/database"
	"github.com/araihu/xisnove/internal/adapters/httpapi"
	"github.com/araihu/xisnove/internal/adapters/ids"
	"github.com/araihu/xisnove/sdk"
)

func TestDiscoveryAPIStorageMatrix(t *testing.T) {
	t.Run("SQLite", func(t *testing.T) {
		runDiscoveryAPIJourney(t, newFileStorageHarness(t, database.ProfileSQLite))
	})
	t.Run("TursoLocal", func(t *testing.T) {
		runDiscoveryAPIJourney(t, newFileStorageHarness(t, database.ProfileTursoLocal))
	})
	t.Run("Postgres", func(t *testing.T) {
		runDiscoveryAPIJourney(t, newPostgresStorageHarness(t))
	})
	t.Run("TursoCloud", func(t *testing.T) {
		runDiscoveryAPIJourney(t, newTursoCloudStorageHarness(t))
	})
}

func runDiscoveryAPIJourney(t *testing.T, harness *storageHarness) {
	t.Helper()
	ctx := context.Background()
	store := harness.primary.Store
	tokens := xiscrypto.NewProductionTokenIssuer()
	const (
		adminEmail    = "discovery-admin@example.com"
		adminPassword = "correct horse battery staple"
	)
	now := time.Now().UTC().Truncate(time.Millisecond)
	auth := application.NewAuthService(application.AuthServiceConfig{
		Store: store, Passwords: xiscrypto.NewProductionPasswordHasher(), Tokens: tokens,
		SessionDuration: time.Hour, Now: func() time.Time { return now }, NewID: ids.NewUUID,
	})
	if err := auth.BootstrapAdmin(ctx, adminEmail, adminPassword); err != nil {
		t.Fatal(err)
	}
	agents := application.NewAgentService(application.AgentServiceConfig{
		Store: store, Tokens: tokens, Now: func() time.Time { return now }, NewID: ids.NewUUID,
	})
	cursors, err := application.NewHMACCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	management := application.NewManagementService(application.ManagementServiceConfig{
		Store: store, Cursors: cursors, Tokens: tokens, NewID: ids.NewUUID,
	})
	discovery := application.NewDiscoveryService(application.DiscoveryServiceConfig{
		Store: harness.primary.DiscoveryUnitOfWork(), IdempotencyStore: store, NewCandidateID: ids.NewUUID,
		NewMonitorID: ids.NewUUID, Now: func() time.Time { return now }, Cursors: cursors,
	})
	handler, err := httpapi.NewHandler(httpapi.HandlerConfig{
		Server: httpapi.NewServer(httpapi.ServerConfig{
			Auth: auth, Configuration: application.NewConfigurationService(store, func() time.Time { return now }, ids.NewUUID),
			Agents: agents, Management: management, Discovery: discovery,
		}),
		Ready: harness.primary.Ready,
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
		Email: openapi_types.Email(adminEmail), Password: pointer(adminPassword),
	})
	if err != nil || session.JSON201 == nil {
		t.Fatalf("create session response=%#v err=%v", session, err)
	}
	adminAuth := bearer(session.JSON201.Token)
	location, err := client.CreateLocationWithResponse(ctx, nil, sdk.CreateLocationRequest{Name: "kube-prod"}, adminAuth)
	if err != nil || location.JSON201 == nil {
		t.Fatalf("create location response=%#v err=%v", location, err)
	}
	enrollment, err := client.CreateAgentEnrollmentTokenWithResponse(ctx, nil, sdk.CreateAgentEnrollmentTokenRequest{
		LocationId: location.JSON201.Id, ExpiresInSeconds: 300,
	}, adminAuth)
	if err != nil || enrollment.JSON201 == nil {
		t.Fatalf("create enrollment response=%#v err=%v", enrollment, err)
	}
	enrolled, err := client.EnrollAgentWithResponse(ctx, sdk.EnrollAgentRequest{
		Token: pointer(enrollment.JSON201.Token), Name: "kube-prod-discovery",
		Capabilities: []sdk.AgentCapability{sdk.AgentCapabilityKubernetesDiscovery},
	})
	if err != nil || enrolled.JSON201 == nil {
		t.Fatalf("enroll agent response=%#v err=%v", enrolled, err)
	}
	agentAuth := bearer(enrolled.JSON201.Credential)
	observedAt := now.Add(-time.Minute)
	input := sdk.DiscoveryCandidateInput{
		SourceKind: "httproute", SourceUid: "uid-route-123", Namespace: "monitoring", Name: "status-api",
		Labels:   map[string]string{"app.kubernetes.io/name": "status-api", "xisnove.dev/exposure": "tailscale"},
		Protocol: sdk.DiscoveryCandidateInputProtocolHttp, Target: "https://status-api.monitoring.svc/ready",
		NetworkPerspective: "cluster:kube-prod", Present: true, ObservedAt: observedAt,
	}
	duplicateKey := sdk.IdempotencyKey("discovery-duplicate-snapshot")
	duplicate, err := client.UpsertDiscoveryCandidatesWithResponse(ctx,
		&sdk.UpsertDiscoveryCandidatesParams{IdempotencyKey: &duplicateKey},
		sdk.DiscoveryCandidateBatch{Candidates: []sdk.DiscoveryCandidateInput{input, input}}, agentAuth,
	)
	if err != nil || duplicate.ApplicationproblemJSONDefault == nil || duplicate.StatusCode() != 400 ||
		duplicate.ApplicationproblemJSONDefault.Code != "validation_failed" {
		t.Fatalf("duplicate discovery identity was not rejected: response=%#v err=%v", duplicate, err)
	}
	uploadKey := sdk.IdempotencyKey("discovery-snapshot-1")
	uploaded, err := client.UpsertDiscoveryCandidatesWithResponse(ctx,
		&sdk.UpsertDiscoveryCandidatesParams{IdempotencyKey: &uploadKey},
		sdk.DiscoveryCandidateBatch{Candidates: []sdk.DiscoveryCandidateInput{input}}, agentAuth,
	)
	if err != nil || uploaded.JSON200 == nil || uploaded.JSON200.Accepted != 1 ||
		uploaded.JSON200.Created != 1 || uploaded.JSON200.Updated != 0 {
		t.Fatalf("upload discovery candidate response=%#v err=%v", uploaded, err)
	}

	present := true
	listed, err := client.ListDiscoveryCandidatesWithResponse(ctx,
		&sdk.ListDiscoveryCandidatesParams{Present: &present}, adminAuth,
	)
	if err != nil || listed.JSON200 == nil || len(listed.JSON200.Items) != 1 {
		t.Fatalf("list discovery candidates response=%#v err=%v", listed, err)
	}
	candidate := listed.JSON200.Items[0]
	assertDiscoveryCandidate(t, candidate, *enrolled.JSON201, *location.JSON201, input, true, sdk.DiscoveryCandidateStatePending)
	got, err := client.GetDiscoveryCandidateWithResponse(ctx, candidate.Id, adminAuth)
	if err != nil || got.JSON200 == nil {
		t.Fatalf("get discovery candidate response=%#v err=%v", got, err)
	}
	assertDiscoveryCandidate(t, *got.JSON200, *enrolled.JSON201, *location.JSON201, input, true, sdk.DiscoveryCandidateStatePending)

	promotionKey := sdk.IdempotencyKey("promote-status-api")
	promotionRequest := sdk.PromotionRequest{
		Name: "Kubernetes status API", Description: pointer("Discovered from HTTPRoute"),
		Labels: pointer(map[string]string{"environment": "homelab"}), Public: false,
		LocationId: location.JSON201.Id, RequiredLocation: true,
		IntervalSeconds: 30, TimeoutMillis: 5000, FailureThreshold: 2, RecoveryThreshold: 2,
	}
	promoted, err := client.PromoteDiscoveryCandidateWithResponse(ctx, candidate.Id,
		&sdk.PromoteDiscoveryCandidateParams{IdempotencyKey: &promotionKey}, promotionRequest, adminAuth,
	)
	if err != nil || promoted.JSON201 == nil {
		t.Fatalf("promote discovery candidate response=%#v err=%v", promoted, err)
	}
	monitorID := promoted.JSON201.Monitor.Id
	if promoted.JSON201.Candidate.PromotedMonitorId == nil || *promoted.JSON201.Candidate.PromotedMonitorId != monitorID ||
		promoted.JSON201.Candidate.State != sdk.DiscoveryCandidateStatePromoted {
		t.Fatalf("promotion did not link candidate and monitor: %#v", promoted.JSON201)
	}
	replayed, err := client.PromoteDiscoveryCandidateWithResponse(ctx, candidate.Id,
		&sdk.PromoteDiscoveryCandidateParams{IdempotencyKey: &promotionKey}, promotionRequest, adminAuth,
	)
	if err != nil || replayed.JSON201 == nil || replayed.JSON201.Monitor.Id != monitorID ||
		replayed.JSON201.Candidate.PromotedMonitorId == nil || *replayed.JSON201.Candidate.PromotedMonitorId != monitorID {
		t.Fatalf("repeat promotion was not idempotent: response=%#v err=%v", replayed, err)
	}
	changedPromotion := promotionRequest
	changedPromotion.Name = "Changed body must conflict"
	changedReplay, err := client.PromoteDiscoveryCandidateWithResponse(ctx, candidate.Id,
		&sdk.PromoteDiscoveryCandidateParams{IdempotencyKey: &promotionKey}, changedPromotion, adminAuth,
	)
	if err != nil || changedReplay.ApplicationproblemJSONDefault == nil || changedReplay.StatusCode() != 409 ||
		changedReplay.ApplicationproblemJSONDefault.Code != "idempotency_key_reused" {
		t.Fatalf("changed promotion replay did not conflict: response=%#v err=%v", changedReplay, err)
	}
	secondInput := input
	secondInput.SourceUid = "uid-route-456"
	secondInput.Name = "second-status-api"
	secondInput.Target = "https://second-status-api.monitoring.svc/ready"
	secondUploadKey := sdk.IdempotencyKey("discovery-snapshot-second-candidate")
	secondUpload, err := client.UpsertDiscoveryCandidatesWithResponse(ctx,
		&sdk.UpsertDiscoveryCandidatesParams{IdempotencyKey: &secondUploadKey},
		sdk.DiscoveryCandidateBatch{Candidates: []sdk.DiscoveryCandidateInput{secondInput}}, agentAuth,
	)
	if err != nil || secondUpload.JSON200 == nil || secondUpload.JSON200.Created != 1 {
		t.Fatalf("upload second discovery candidate response=%#v err=%v", secondUpload, err)
	}
	allCandidates, err := client.ListDiscoveryCandidatesWithResponse(ctx, nil, adminAuth)
	if err != nil || allCandidates.JSON200 == nil || len(allCandidates.JSON200.Items) != 2 {
		t.Fatalf("list two discovery candidates response=%#v err=%v", allCandidates, err)
	}
	secondCandidateID := allCandidates.JSON200.Items[0].Id
	if secondCandidateID == candidate.Id {
		secondCandidateID = allCandidates.JSON200.Items[1].Id
	}
	crossCandidateReplay, err := client.PromoteDiscoveryCandidateWithResponse(ctx, secondCandidateID,
		&sdk.PromoteDiscoveryCandidateParams{IdempotencyKey: &promotionKey}, promotionRequest, adminAuth,
	)
	if err != nil || crossCandidateReplay.ApplicationproblemJSONDefault == nil ||
		crossCandidateReplay.StatusCode() != 409 || crossCandidateReplay.ApplicationproblemJSONDefault.Code != "idempotency_key_reused" {
		t.Fatalf("cross-candidate idempotency replay did not conflict: response=%#v err=%v", crossCandidateReplay, err)
	}

	tombstone := input
	tombstone.Present = false
	tombstone.ObservedAt = now.Add(time.Minute)
	tombstoneKey := sdk.IdempotencyKey("discovery-snapshot-2")
	tombstoned, err := client.UpsertDiscoveryCandidatesWithResponse(ctx,
		&sdk.UpsertDiscoveryCandidatesParams{IdempotencyKey: &tombstoneKey},
		sdk.DiscoveryCandidateBatch{Candidates: []sdk.DiscoveryCandidateInput{tombstone}}, agentAuth,
	)
	if err != nil || tombstoned.JSON200 == nil || tombstoned.JSON200.Accepted != 1 ||
		tombstoned.JSON200.Created != 0 || tombstoned.JSON200.Updated != 1 {
		t.Fatalf("upload discovery tombstone response=%#v err=%v", tombstoned, err)
	}
	stale, err := client.GetDiscoveryCandidateWithResponse(ctx, candidate.Id, adminAuth)
	if err != nil || stale.JSON200 == nil || stale.JSON200.Present ||
		stale.JSON200.State != sdk.DiscoveryCandidateStatePromoted || stale.JSON200.PromotedMonitorId == nil ||
		*stale.JSON200.PromotedMonitorId != monitorID || !stale.JSON200.FirstSeenAt.Equal(observedAt) ||
		!stale.JSON200.LastObservedAt.Equal(tombstone.ObservedAt) {
		t.Fatalf("promoted tombstone lost state or provenance: response=%#v err=%v", stale, err)
	}
	absent := false
	staleList, err := client.ListDiscoveryCandidatesWithResponse(ctx,
		&sdk.ListDiscoveryCandidatesParams{Present: &absent}, adminAuth,
	)
	if err != nil || staleList.JSON200 == nil || len(staleList.JSON200.Items) != 1 ||
		staleList.JSON200.Items[0].Id != candidate.Id {
		t.Fatalf("list absent discovery candidates response=%#v err=%v", staleList, err)
	}
	preserved, err := client.GetMonitorWithResponse(ctx, monitorID, adminAuth)
	if err != nil || preserved.JSON200 == nil || preserved.JSON200.Id != monitorID ||
		preserved.JSON200.Name != promotionRequest.Name || preserved.JSON200.LocationId != location.JSON201.Id {
		t.Fatalf("tombstone did not preserve promoted monitor: response=%#v err=%v", preserved, err)
	}
}

func assertDiscoveryCandidate(
	t *testing.T,
	candidate sdk.DiscoveryCandidate,
	agent sdk.EnrolledAgent,
	location sdk.Location,
	input sdk.DiscoveryCandidateInput,
	present bool,
	state sdk.DiscoveryCandidateState,
) {
	t.Helper()
	if candidate.AgentId != agent.AgentId || candidate.LocationId != location.Id ||
		candidate.SourceKind != input.SourceKind || candidate.SourceUid != input.SourceUid ||
		candidate.Namespace != input.Namespace || candidate.Name != input.Name ||
		candidate.Protocol != sdk.DiscoveryCandidateProtocol(input.Protocol) || candidate.Target != input.Target ||
		candidate.NetworkPerspective != input.NetworkPerspective || candidate.Present != present || candidate.State != state ||
		!candidate.FirstSeenAt.Equal(input.ObservedAt) || !candidate.LastObservedAt.Equal(input.ObservedAt) ||
		candidate.Labels["app.kubernetes.io/name"] != "status-api" ||
		candidate.Labels["xisnove.dev/exposure"] != "tailscale" {
		t.Fatalf("discovery candidate did not preserve canonical provenance: %#v", candidate)
	}
}
