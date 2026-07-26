package httpapi

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestManagementMonitorMutationReplaysAndRejectsChangedBody(t *testing.T) {
	server, principal, monitor, _ := managementMutationHTTPServer(t)
	ctx := ContextWithPrincipal(context.Background(), principal)
	key := IdempotencyKey("replace-monitor")
	body := replacementMonitorBody(t, monitor.LocationID)
	request := UpdateMonitorRequestObject{
		MonitorId: mustUUID(t, string(monitor.ID)),
		Params:    UpdateMonitorParams{IdempotencyKey: &key}, Body: &body,
	}

	first, err := server.UpdateMonitor(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	firstMonitor, ok := first.(UpdateMonitor200JSONResponse)
	if !ok || firstMonitor.Name != "replaced" || firstMonitor.Enabled {
		t.Fatalf("first response = %#v", first)
	}
	second, err := server.UpdateMonitor(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	secondMonitor, ok := second.(UpdateMonitor200JSONResponse)
	if !ok || secondMonitor.Id != firstMonitor.Id || !secondMonitor.UpdatedAt.Equal(firstMonitor.UpdatedAt) {
		t.Fatalf("replay response = %#v", second)
	}

	changed := body
	changed.Name = "different"
	request.Body = &changed
	conflict, err := server.UpdateMonitor(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	problem, ok := conflict.(UpdateMonitordefaultApplicationProblemPlusJSONResponse)
	if !ok || problem.StatusCode != 409 || problem.Body.Code != "idempotency_key_reused" {
		t.Fatalf("changed-body response = %#v", conflict)
	}
}

func TestManagementLocationMutationRequiresPrincipalBodyAndIdempotencyKey(t *testing.T) {
	server, principal, _, _ := managementMutationHTTPServer(t)
	id := mustUUID(t, managementReadID1)

	unauthorized, err := server.UpdateLocation(context.Background(), UpdateLocationRequestObject{LocationId: id})
	if err != nil {
		t.Fatal(err)
	}
	if problem := unauthorized.(UpdateLocationdefaultApplicationProblemPlusJSONResponse); problem.StatusCode != 401 {
		t.Fatalf("unauthorized response = %#v", problem)
	}

	ctx := ContextWithPrincipal(context.Background(), principal)
	missingKey, err := server.UpdateLocation(ctx, UpdateLocationRequestObject{LocationId: id})
	if err != nil {
		t.Fatal(err)
	}
	if problem := missingKey.(UpdateLocationdefaultApplicationProblemPlusJSONResponse); problem.Body.Code != "validation_failed" {
		t.Fatalf("missing key response = %#v", problem)
	}

	key := IdempotencyKey("location-update")
	missingBody, err := server.UpdateLocation(ctx, UpdateLocationRequestObject{
		LocationId: id, Params: UpdateLocationParams{IdempotencyKey: &key},
	})
	if err != nil {
		t.Fatal(err)
	}
	if problem := missingBody.(UpdateLocationdefaultApplicationProblemPlusJSONResponse); problem.Body.Code != "validation_failed" {
		t.Fatalf("missing body response = %#v", problem)
	}
}

func TestManagementAgentMutationAndCredentialLifecycle(t *testing.T) {
	server, principal, _, enrolled := managementMutationHTTPServer(t)
	ctx := ContextWithPrincipal(context.Background(), principal)
	agentID := mustUUID(t, string(enrolled.ID))
	key := IdempotencyKey("update-agent")
	capabilities := []AgentCapability{
		AgentCapabilityHttp,
		AgentCapabilityKubernetesDiscovery,
		AgentCapabilityKubernetesWatch,
	}
	name := "discovery-agent"
	updated, err := server.UpdateAgent(ctx, UpdateAgentRequestObject{
		AgentId: agentID, Params: UpdateAgentParams{IdempotencyKey: &key},
		Body: &UpdateAgentJSONRequestBody{Name: &name, Capabilities: &capabilities},
	})
	if err != nil {
		t.Fatal(err)
	}
	agent, ok := updated.(UpdateAgent200JSONResponse)
	if !ok || agent.Name != name || len(agent.Capabilities) != 3 {
		t.Fatalf("updated agent response = %#v", updated)
	}

	rotationKey := IdempotencyKey("rotate-agent")
	rotated, err := server.RotateAgentCredential(ctx, RotateAgentCredentialRequestObject{
		AgentId: agentID, Params: RotateAgentCredentialParams{IdempotencyKey: &rotationKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, ok := rotated.(RotateAgentCredential201JSONResponse)
	if !ok || credential.Credential == nil || *credential.Credential == "" || credential.CredentialGeneration != 2 {
		t.Fatalf("rotation response = %#v", rotated)
	}
	replay, err := server.RotateAgentCredential(ctx, RotateAgentCredentialRequestObject{
		AgentId: agentID, Params: RotateAgentCredentialParams{IdempotencyKey: &rotationKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	if problem, ok := replay.(RotateAgentCredential409ApplicationProblemPlusJSONResponse); !ok || problem.Code != "credential_already_issued" {
		t.Fatalf("rotation replay = %#v", replay)
	}

	newPrincipal, err := server.agents.Authenticate(context.Background(), *credential.Credential)
	if err != nil {
		t.Fatal(err)
	}
	domainCapabilities := []domain.AgentCapability{
		domain.CapabilityHTTP,
		domain.CapabilityKubernetesDiscovery,
		domain.CapabilityKubernetesWatch,
	}
	if err := server.agents.Heartbeat(context.Background(), newPrincipal, 2, "v2", domainCapabilities); err != nil {
		t.Fatal(err)
	}

	revoked, err := server.RevokeAgentCredentialGeneration(ctx, RevokeAgentCredentialGenerationRequestObject{
		AgentId: agentID, Generation: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := revoked.(RevokeAgentCredentialGeneration204Response); !ok {
		t.Fatalf("generation revoke = %#v", revoked)
	}
	repeated, err := server.RevokeAgentCredentialGeneration(ctx, RevokeAgentCredentialGenerationRequestObject{
		AgentId: agentID, Generation: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := repeated.(RevokeAgentCredentialGeneration204Response); !ok {
		t.Fatalf("repeated generation revoke = %#v", repeated)
	}
	current, err := server.RevokeAgentCredentialGeneration(ctx, RevokeAgentCredentialGenerationRequestObject{
		AgentId: agentID, Generation: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if problem, ok := current.(RevokeAgentCredentialGeneration409ApplicationProblemPlusJSONResponse); !ok || problem.Code != "conflict" {
		t.Fatalf("current generation revoke = %#v", current)
	}

	if response, err := server.RevokeAgent(ctx, RevokeAgentRequestObject{AgentId: agentID}); err != nil {
		t.Fatal(err)
	} else if _, ok := response.(RevokeAgent204Response); !ok {
		t.Fatalf("agent revoke = %#v", response)
	}
	if response, err := server.RevokeAgent(ctx, RevokeAgentRequestObject{AgentId: agentID}); err != nil {
		t.Fatal(err)
	} else if _, ok := response.(RevokeAgent204Response); !ok {
		t.Fatalf("agent repeat revoke = %#v", response)
	}
	missing, err := server.RevokeAgent(ctx, RevokeAgentRequestObject{AgentId: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if problem, ok := missing.(RevokeAgentdefaultApplicationProblemPlusJSONResponse); !ok || problem.StatusCode != 404 {
		t.Fatalf("missing agent revoke = %#v", missing)
	}
}

func managementMutationHTTPServer(t *testing.T) (*Server, application.Principal, application.ConfiguredMonitor, application.EnrolledAgentCredential) {
	t.Helper()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "management-http.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(db)
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	configuration := application.NewConfigurationService(store, func() time.Time { return now }, uuid.NewString)
	location, err := configuration.CreateLocation(ctx, application.CreateLocationCommand{Name: "edge"})
	if err != nil {
		t.Fatal(err)
	}
	monitor, err := configuration.CreateMonitor(ctx, application.CreateMonitorCommand{
		Name: "original", LocationID: location.ID, RequiredLocation: true,
		Interval: time.Minute, Timeout: 5 * time.Second, FailureThreshold: 2, RecoveryThreshold: 2,
		Probe: domain.ProbeDefinition{Kind: domain.MonitorKindHTTP, HTTP: domain.HTTPProbe{
			Method: "GET", URL: "https://example.test/health",
			ExpectedStatus: []domain.StatusRange{{Min: 200, Max: 299}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	codec, err := application.NewHMACCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	tokens := xiscrypto.NewProductionTokenIssuer()
	agents := application.NewAgentService(application.AgentServiceConfig{
		Store: store, Tokens: tokens, Now: func() time.Time { return now }, NewID: uuid.NewString,
	})
	enrollment, err := agents.CreateEnrollmentToken(ctx, location.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	enrolled, err := agents.Enroll(ctx, application.EnrollAgentCommand{
		Token: enrollment.Token, Name: "agent",
		Capabilities: []domain.AgentCapability{domain.CapabilityHTTP, domain.CapabilityKubernetesDiscovery},
	})
	if err != nil {
		t.Fatal(err)
	}
	management := application.NewManagementService(application.ManagementServiceConfig{
		Store: store, Cursors: codec, Tokens: tokens, NewID: uuid.NewString,
	})
	principal := application.Principal{
		Kind: application.PrincipalAdmin, SubjectID: uuid.NewString(),
		CredentialKind: application.CredentialSession, CredentialID: uuid.NewString(),
	}
	return NewServer(ServerConfig{Configuration: configuration, Agents: agents, Management: management}), principal, monitor, enrolled
}

func replacementMonitorBody(t *testing.T, locationID domain.LocationID) UpdateMonitorJSONRequestBody {
	t.Helper()
	var probe ProbeDefinition
	if err := probe.FromHTTPProbeDefinition(HTTPProbeDefinition{
		Kind: "http", Method: "GET", Url: "https://example.test/ready",
		ExpectedStatus: []StatusRange{{Minimum: 200, Maximum: 299}},
	}); err != nil {
		t.Fatal(err)
	}
	return UpdateMonitorJSONRequestBody{
		Name: "replaced", Description: "replacement", Labels: map[string]string{"tier": "edge"},
		DisplayOrder: 2, Public: true, Enabled: false,
		IntervalSeconds: 60, TimeoutMillis: 5000, FailureThreshold: 2, RecoveryThreshold: 3,
		LocationId: mustUUID(t, string(locationID)), RequiredLocation: true, Probe: probe,
	}
}
