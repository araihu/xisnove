package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	"github.com/araihu/xisnove/internal/adapters/httpapi"
	"github.com/araihu/xisnove/internal/adapters/ids"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
	"github.com/araihu/xisnove/sdk"
)

func TestOperatorAPIOwnershipCredentialReplayAndScopeBoundaries(t *testing.T) {
	ctx := context.Background()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "operator-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(db)
	now := time.Now().UTC().Truncate(time.Millisecond)
	tokens := xiscrypto.NewProductionTokenIssuer()
	auth := application.NewAuthService(application.AuthServiceConfig{
		Store: store, Passwords: xiscrypto.NewProductionPasswordHasher(), Tokens: tokens,
		SessionDuration: time.Hour, Now: func() time.Time { return now }, NewID: ids.NewUUID,
	})
	const password = "correct horse battery staple"
	if err := auth.BootstrapAdmin(ctx, "operator-api@example.com", password); err != nil {
		t.Fatal(err)
	}
	configuration := application.NewConfigurationService(store, func() time.Time { return now }, ids.NewUUID)
	apiTokens := application.NewAPITokenService(application.APITokenServiceConfig{
		Store: store, Tokens: tokens, Now: func() time.Time { return now }, NewID: ids.NewUUID,
	})
	handler, err := httpapi.NewHandler(httpapi.HandlerConfig{
		Server: httpapi.NewServer(httpapi.ServerConfig{
			Auth: auth, APITokens: apiTokens, Configuration: configuration,
			Operator: application.OperatorService{Store: store, Credentials: tokens},
		}),
		Ready: func(context.Context) error { return nil },
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
	session, err := client.CreateSessionWithResponse(ctx, sdk.CreateSessionRequest{Email: "operator-api@example.com", Password: pointer(password)})
	if err != nil || session.JSON201 == nil {
		t.Fatalf("session = %#v, err = %v", session, err)
	}
	adminAuth := bearer(session.JSON201.Token)
	location, err := client.CreateLocationWithResponse(ctx, nil, sdk.CreateLocationRequest{Name: "edge"}, adminAuth)
	if err != nil || location.JSON201 == nil {
		t.Fatalf("location = %#v, err = %v", location, err)
	}
	issued, err := client.CreateAPITokenWithResponse(ctx, nil, sdk.CreateAPITokenRequest{
		Name: "operator", Scopes: []sdk.APITokenScope{"operator:provision"},
	}, adminAuth)
	if err != nil || issued.JSON201 == nil {
		t.Fatalf("operator token = %#v, err = %v", issued, err)
	}
	operatorAuth := bearer(issued.JSON201.Token)
	owner := sdk.ExternalOwner{Key: "monitoring.xisnove.io/Agent/default/edge", Uid: "edge-uid-1"}
	initialCredential := "initial-credential-012345678901234567890123456789"
	validatorCredential := strings.Repeat("validator-credential-sentinel-", 80)
	validatorBody, err := json.Marshal(map[string]any{
		"owner": map[string]string{"key": owner.Key, "uid": owner.Uid},
		"name":  "edge", "locationId": location.JSON201.Id, "enabled": true,
		"capabilities":      []string{"http"},
		"initialCredential": map[string]any{"generation": 1, "credential": validatorCredential},
	})
	if err != nil {
		t.Fatal(err)
	}
	validatorRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/operator/agents:apply", bytes.NewReader(validatorBody))
	if err != nil {
		t.Fatal(err)
	}
	validatorRequest.Header.Set("Content-Type", "application/json")
	validatorRequest.Header.Set("Idempotency-Key", "operator-validator-oversized")
	validatorResponse, err := server.Client().Do(validatorRequest)
	if err != nil {
		t.Fatal(err)
	}
	validatorResponseBody, err := io.ReadAll(validatorResponse.Body)
	_ = validatorResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var validatorProblem sdk.Problem
	if err := json.Unmarshal(validatorResponseBody, &validatorProblem); err != nil {
		t.Fatal(err)
	}
	if validatorResponse.StatusCode != http.StatusBadRequest || validatorProblem.Type != "https://xisnove.dev/problems/validation" || validatorProblem.Title != "Request validation failed" || validatorProblem.Detail != nil || validatorProblem.Instance != nil || strings.Contains(string(validatorResponseBody), "validator-credential-sentinel-") {
		t.Fatalf("validator response = status:%d problem:%#v", validatorResponse.StatusCode, validatorProblem)
	}
	apply := sdk.ApplyOperatorAgentRequest{
		Owner: owner, Name: "edge", LocationId: location.JSON201.Id, Enabled: true,
		Capabilities:      []sdk.AgentCapability{sdk.AgentCapabilityHttp},
		InitialCredential: &sdk.OperatorInitialCredential{Generation: 1, Credential: pointer(initialCredential)},
	}
	applyKey := sdk.RequiredIdempotencyKey("operator-agent-apply-1")
	denied, err := client.ApplyOperatorAgentWithResponse(ctx, &sdk.ApplyOperatorAgentParams{IdempotencyKey: applyKey}, apply, adminAuth)
	if err != nil || denied.StatusCode() != http.StatusForbidden {
		t.Fatalf("administrator reached operator API: response=%#v err=%v", denied, err)
	}
	badOwner := apply
	badOwner.Owner.Uid = " "
	bad, err := client.ApplyOperatorAgentWithResponse(ctx, &sdk.ApplyOperatorAgentParams{IdempotencyKey: sdk.RequiredIdempotencyKey("operator-agent-malformed-owner")}, badOwner, operatorAuth)
	if err != nil || bad.StatusCode() != http.StatusBadRequest || bad.ApplicationproblemJSONDefault == nil {
		t.Fatalf("malformed owner = %#v, err = %v", bad, err)
	}
	if strings.Contains(string(bad.Body), initialCredential) || (bad.ApplicationproblemJSONDefault.Detail != nil && len(*bad.ApplicationproblemJSONDefault.Detail) > 256) || (bad.ApplicationproblemJSONDefault.Instance != nil && len(*bad.ApplicationproblemJSONDefault.Instance) > 512) {
		t.Fatalf("malformed owner problem leaked or was unbounded: %#v", bad.ApplicationproblemJSONDefault)
	}
	first, err := client.ApplyOperatorAgentWithResponse(ctx, &sdk.ApplyOperatorAgentParams{IdempotencyKey: applyKey}, apply, operatorAuth)
	if err != nil || first.JSON200 == nil || first.JSON200.CredentialGeneration != 1 || strings.Contains(string(first.Body), initialCredential) {
		t.Fatalf("agent apply = %#v, err = %v", first, err)
	}
	firstID := first.JSON200.ExternalId
	replayed, err := client.ApplyOperatorAgentWithResponse(ctx, &sdk.ApplyOperatorAgentParams{IdempotencyKey: applyKey}, apply, operatorAuth)
	if err != nil || replayed.JSON200 == nil || replayed.JSON200.ExternalId != firstID || strings.Contains(string(replayed.Body), initialCredential) {
		t.Fatalf("lost response replay = %#v, err = %v", replayed, err)
	}
	changed := apply
	changed.InitialCredential.Credential = pointer("different-initial-credential-012345678901234567890")
	conflict, err := client.ApplyOperatorAgentWithResponse(ctx, &sdk.ApplyOperatorAgentParams{IdempotencyKey: applyKey}, changed, operatorAuth)
	if err != nil || conflict.StatusCode() != http.StatusConflict || conflict.ApplicationproblemJSON409 == nil || conflict.ApplicationproblemJSON409.Code != "idempotency_key_reused" || strings.Contains(string(conflict.Body), "different-initial") {
		t.Fatalf("changed hash conflict = %#v, err = %v", conflict, err)
	}
	putCredential := "replacement-credential-0123456789012345678901234567"
	put, err := client.PutOperatorAgentCredentialWithResponse(ctx, firstID, 2,
		&sdk.PutOperatorAgentCredentialParams{IdempotencyKey: sdk.RequiredIdempotencyKey("operator-agent-put-2")},
		sdk.PutOperatorAgentCredentialRequest{Owner: owner, Credential: pointer(putCredential)}, operatorAuth)
	if err != nil || put.StatusCode() != http.StatusNoContent || len(put.Body) != 0 {
		t.Fatalf("put credential = %#v, err = %v", put, err)
	}
	revokeRequest := sdk.RevokeOperatorAgentCredentialRequest{Owner: owner}
	revokedTooSoon, err := client.RevokeOperatorAgentCredentialWithResponse(ctx, firstID, 1,
		&sdk.RevokeOperatorAgentCredentialParams{IdempotencyKey: sdk.RequiredIdempotencyKey("operator-agent-revoke-1")}, revokeRequest, operatorAuth)
	if err != nil || revokedTooSoon.StatusCode() != http.StatusConflict {
		t.Fatalf("revoke before heartbeat = %#v, err = %v", revokedTooSoon, err)
	}
	if err := store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		updated, err := repositories.Agents.UpdateHeartbeat(ctx, domain.AgentID(firstID.String()), 2, "v1", []domain.AgentCapability{domain.CapabilityHTTP}, now.Add(time.Minute))
		if err != nil || !updated {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	revoked, err := client.RevokeOperatorAgentCredentialWithResponse(ctx, firstID, 1,
		&sdk.RevokeOperatorAgentCredentialParams{IdempotencyKey: sdk.RequiredIdempotencyKey("operator-agent-revoke-2")}, revokeRequest, operatorAuth)
	if err != nil || revoked.StatusCode() != http.StatusNoContent || len(revoked.Body) != 0 {
		t.Fatalf("revoke after heartbeat = %#v, err = %v", revoked, err)
	}
	recreated := apply
	recreated.Owner.Uid = "edge-uid-2"
	recreated.InitialCredential.Credential = pointer("recreated-credential-012345678901234567890123456789")
	recreatedResult, err := client.ApplyOperatorAgentWithResponse(ctx, &sdk.ApplyOperatorAgentParams{IdempotencyKey: applyKey}, recreated, operatorAuth)
	if err != nil || recreatedResult.JSON200 == nil || recreatedResult.JSON200.ExternalId == firstID {
		t.Fatalf("recreated UID did not receive new resource = %#v, err = %v", recreatedResult, err)
	}
	oldID := firstID
	oldDelete, err := client.DeleteOperatorAgentWithResponse(ctx,
		&sdk.DeleteOperatorAgentParams{IdempotencyKey: sdk.RequiredIdempotencyKey("operator-agent-recreated-old-id")},
		sdk.DeleteOperatorAgentRequest{Owner: recreated.Owner, ExternalId: &oldID}, operatorAuth)
	if err != nil || oldDelete.StatusCode() != http.StatusConflict {
		t.Fatalf("recreated UID adopted orphan = %#v, err = %v", oldDelete, err)
	}
	deleteKey := sdk.RequiredIdempotencyKey("operator-agent-owner-only-delete")
	deleted, err := client.DeleteOperatorAgentWithResponse(ctx, &sdk.DeleteOperatorAgentParams{IdempotencyKey: deleteKey}, sdk.DeleteOperatorAgentRequest{Owner: recreated.Owner}, operatorAuth)
	if err != nil || deleted.StatusCode() != http.StatusNoContent || len(deleted.Body) != 0 {
		t.Fatalf("owner-only delete = %#v, err = %v", deleted, err)
	}
	deleteReplay, err := client.DeleteOperatorAgentWithResponse(ctx, &sdk.DeleteOperatorAgentParams{IdempotencyKey: deleteKey}, sdk.DeleteOperatorAgentRequest{Owner: recreated.Owner}, operatorAuth)
	if err != nil || deleteReplay.StatusCode() != http.StatusNoContent {
		t.Fatalf("owner-only delete replay = %#v, err = %v", deleteReplay, err)
	}
	var probe sdk.ProbeDefinition
	if err := probe.FromHTTPProbeDefinition(sdk.HTTPProbeDefinition{
		Kind: sdk.HTTPProbeDefinitionKindHttp, Method: sdk.GET, Url: "https://example.test/health",
		Headers: map[string]string{}, Body: []byte{}, ExpectedStatus: []sdk.StatusRange{{Minimum: 200, Maximum: 299}},
		BodyContains: []string{}, BodyDoesNotContain: []string{}, FollowRedirects: true,
	}); err != nil {
		t.Fatal(err)
	}
	monitorOwner := sdk.ExternalOwner{Key: "monitoring.xisnove.io/Monitor/default/health", Uid: "monitor-uid-1"}
	monitor, err := client.ApplyOperatorMonitorWithResponse(ctx,
		&sdk.ApplyOperatorMonitorParams{IdempotencyKey: sdk.RequiredIdempotencyKey("operator-monitor-apply-1")},
		sdk.ApplyOperatorMonitorRequest{Owner: monitorOwner, Monitor: sdk.UpdateMonitorRequest{
			Name: "health", Description: "", Labels: map[string]string{}, DisplayOrder: 0, Public: false, Enabled: true,
			IntervalSeconds: 60, TimeoutMillis: 5000, FailureThreshold: 2, RecoveryThreshold: 1,
			LocationId: location.JSON201.Id, RequiredLocation: true, Probe: probe,
		}}, operatorAuth)
	if err != nil || monitor.JSON200 == nil || monitor.JSON200.ExternalId.String() == "" || len(monitor.Body) == 0 {
		t.Fatalf("monitor apply = %#v, err = %v", monitor, err)
	}
	ticks, err := store.Repositories().StateTicks.ListStateTicks(
		ctx, domain.MonitorID(monitor.JSON200.ExternalId.String()), now.Add(-time.Minute), now.Add(time.Minute), 10,
	)
	if err != nil || len(ticks) != 1 {
		t.Fatalf("operator monitor initial ticks = %#v, err=%v", ticks, err)
	}
	initialTick := ticks[0]
	if initialTick.LocationID == nil || *initialTick.LocationID != domain.LocationID(location.JSON201.Id.String()) ||
		initialTick.Lifecycle != domain.MonitorLifecycleActive || initialTick.Health != domain.HealthPending ||
		initialTick.ReasonCode != domain.StateTickReasonInitial || initialTick.Actor.Kind != domain.StateTickActorSystem {
		t.Fatalf("operator monitor initial tick = %#v", initialTick)
	}
	monitorDelete, err := client.DeleteOperatorMonitorWithResponse(ctx,
		&sdk.DeleteOperatorMonitorParams{IdempotencyKey: sdk.RequiredIdempotencyKey("operator-monitor-owner-only-delete")},
		sdk.DeleteOperatorMonitorRequest{Owner: monitorOwner}, operatorAuth)
	if err != nil || monitorDelete.StatusCode() != http.StatusNoContent || len(monitorDelete.Body) != 0 {
		t.Fatalf("monitor owner-only delete = %#v, err = %v", monitorDelete, err)
	}
}
