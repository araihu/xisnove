package mockapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/araihu/xisnove/internal/mockapi"
)

const (
	adminEmail    = "admin@xisnove.test"
	adminPassword = "mock-password"
	agentToken    = "xisnove_mock_agent_000000000000000000000001"
)

func TestLoginLogoutRevokesTheSession(t *testing.T) {
	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()

	session := login(t, server.URL)
	response := request(t, server.URL, http.MethodGet, "/v1/monitors", session, nil, nil)
	assertStatus(t, response, http.StatusOK)

	response = request(t, server.URL, http.MethodDelete, "/v1/sessions/current", session, nil, nil)
	assertStatus(t, response, http.StatusNoContent)

	response = request(t, server.URL, http.MethodGet, "/v1/monitors", session, nil, nil)
	assertProblem(t, response, http.StatusUnauthorized, "unauthorized")
}

func TestAPITokenScopesCanBeUpdatedAndRevoked(t *testing.T) {
	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()
	session := login(t, server.URL)

	response := request(t, server.URL, http.MethodPost, "/v1/api-tokens", session, map[string]any{
		"name":   "UI read only",
		"scopes": []string{"monitors:read"},
	}, map[string]string{"Idempotency-Key": "token-ui-read-1"})
	assertStatus(t, response, http.StatusCreated)
	created := decodeObject(t, response)
	token := created["token"].(string)
	apiToken := created["apiToken"].(map[string]any)
	tokenID := apiToken["id"].(string)

	response = request(t, server.URL, http.MethodGet, "/v1/monitors", token, nil, nil)
	assertStatus(t, response, http.StatusOK)
	response = request(t, server.URL, http.MethodPost, "/v1/monitors", token, monitorInput("forbidden"), nil)
	assertProblem(t, response, http.StatusForbidden, "insufficient_scope")

	response = request(t, server.URL, http.MethodPatch, "/v1/api-tokens/"+tokenID, session, map[string]any{
		"scopes": []string{"monitors:read", "monitors:write"},
	}, map[string]string{"Idempotency-Key": "token-ui-write-1"})
	assertStatus(t, response, http.StatusOK)
	response = request(t, server.URL, http.MethodPost, "/v1/monitors", token, monitorInput("allowed"), nil)
	assertStatus(t, response, http.StatusCreated)

	response = request(t, server.URL, http.MethodDelete, "/v1/api-tokens/"+tokenID, session, nil, nil)
	assertStatus(t, response, http.StatusNoContent)
	response = request(t, server.URL, http.MethodGet, "/v1/monitors", token, nil, nil)
	assertProblem(t, response, http.StatusUnauthorized, "unauthorized")
}

func TestFixtureAPITokensUseStoredScopesAndRevocation(t *testing.T) {
	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()
	session := login(t, server.URL)

	response := request(t, server.URL, http.MethodGet, "/v1/monitors", mockapi.FixtureReadOnlyAPIToken, nil, nil)
	assertStatus(t, response, http.StatusOK)
	response = request(t, server.URL, http.MethodPost, "/v1/monitors", mockapi.FixtureReadOnlyAPIToken, monitorInput("read-only"), nil)
	assertProblem(t, response, http.StatusForbidden, "insufficient_scope")

	response = request(t, server.URL, http.MethodPost, "/v1/monitors", mockapi.FixtureFullAPIToken, monitorInput("full-access"), nil)
	assertStatus(t, response, http.StatusCreated)
	response = request(
		t,
		server.URL,
		http.MethodDelete,
		"/v1/api-tokens/00000000-0000-4100-8000-000000000001",
		session,
		nil,
		nil,
	)
	assertStatus(t, response, http.StatusNoContent)
	response = request(t, server.URL, http.MethodGet, "/v1/monitors", mockapi.FixtureFullAPIToken, nil, nil)
	assertProblem(t, response, http.StatusUnauthorized, "unauthorized")
}

func TestMonitorsAreCursorPagedAndIdempotent(t *testing.T) {
	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()
	session := login(t, server.URL)

	first := request(t, server.URL, http.MethodGet, "/v1/monitors?limit=1", session, nil, nil)
	assertStatus(t, first, http.StatusOK)
	firstPage := decodeObject(t, first)
	page := firstPage["page"].(map[string]any)
	if len(firstPage["items"].([]any)) != 1 || page["nextCursor"] == "" {
		t.Fatalf("first page = %#v", firstPage)
	}
	cursor := page["nextCursor"].(string)
	second := request(t, server.URL, http.MethodGet, "/v1/monitors?limit=1&cursor="+cursor, session, nil, nil)
	assertStatus(t, second, http.StatusOK)
	secondPage := decodeObject(t, second)
	if firstPage["items"].([]any)[0].(map[string]any)["id"] ==
		secondPage["items"].([]any)[0].(map[string]any)["id"] {
		t.Fatal("cursor returned the first monitor twice")
	}

	headers := map[string]string{"Idempotency-Key": "monitor-create-1"}
	created := request(t, server.URL, http.MethodPost, "/v1/monitors", session, monitorInput("idempotent"), headers)
	assertStatus(t, created, http.StatusCreated)
	createdMonitor := decodeObject(t, created)
	replayed := request(t, server.URL, http.MethodPost, "/v1/monitors", session, monitorInput("idempotent"), headers)
	assertStatus(t, replayed, http.StatusCreated)
	replayedMonitor := decodeObject(t, replayed)
	if createdMonitor["id"] != replayedMonitor["id"] {
		t.Fatalf("idempotency changed ID from %v to %v", createdMonitor["id"], replayedMonitor["id"])
	}
}

func TestMonitorStateHistoryFixturePreservesProvenance(t *testing.T) {
	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()

	response := request(
		t,
		server.URL,
		http.MethodGet,
		"/v1/monitors/00000000-0000-4200-8000-000000000101/state-ticks",
		mockapi.FixtureReadOnlyAPIToken,
		nil,
		nil,
	)
	assertStatus(t, response, http.StatusOK)
	body := decodeObject(t, response)
	ticks, ok := body["ticks"].([]any)
	if !ok || len(ticks) != 3 {
		t.Fatalf("state history ticks = %#v", body["ticks"])
	}
	first := ticks[0].(map[string]any)
	last := ticks[2].(map[string]any)
	if first["health"] != "up" || first["reasonCode"] != "probe_success" {
		t.Fatalf("first state tick = %#v", first)
	}
	if last["health"] != "unknown" || last["reasonCode"] != "dependency_paused" {
		t.Fatalf("last state tick = %#v", last)
	}
	actor := last["actor"].(map[string]any)
	if actor["kind"] != "user" || last["userActionId"] == nil || last["causalTickId"] == nil {
		t.Fatalf("last provenance = %#v", last)
	}

	unauthorized := request(
		t,
		server.URL,
		http.MethodGet,
		"/v1/monitors/00000000-0000-4200-8000-000000000101/state-ticks",
		"",
		nil,
		nil,
	)
	assertProblem(t, unauthorized, http.StatusUnauthorized, "unauthorized")

	missing := request(
		t,
		server.URL,
		http.MethodGet,
		"/v1/monitors/00000000-0000-4200-8000-000000000199/state-ticks",
		mockapi.FixtureReadOnlyAPIToken,
		nil,
		nil,
	)
	assertProblem(t, missing, http.StatusNotFound, "monitor_not_found")
}

func TestIncidentsDiscoveryNotificationsAndPublicStatusFixtures(t *testing.T) {
	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()
	session := login(t, server.URL)

	incidents := request(t, server.URL, http.MethodGet, "/v1/incidents", session, nil, nil)
	assertStatus(t, incidents, http.StatusOK)
	incidentPage := decodeObject(t, incidents)
	incident := incidentPage["items"].([]any)[0].(map[string]any)
	events := request(
		t,
		server.URL,
		http.MethodGet,
		"/v1/incidents/"+incident["id"].(string)+"/events",
		session,
		nil,
		nil,
	)
	assertStatus(t, events, http.StatusOK)

	batch := request(t, server.URL, http.MethodPost, "/v1/agent/discovery-candidates:batch", agentToken, map[string]any{
		"candidates": []map[string]any{{
			"sourceKind": "service", "sourceUid": "service/default/demo", "namespace": "default",
			"name": "demo", "labels": map[string]string{"namespace": "default"},
			"protocol": "http", "target": "https://demo.example.test/health",
			"networkPerspective": "cluster/default", "present": true,
			"observedAt": "2026-07-25T12:00:00Z",
		}},
		"complete": true, "completedAt": "2026-07-25T12:00:00Z",
	}, map[string]string{"Idempotency-Key": "discovery-demo-1"})
	assertStatus(t, batch, http.StatusOK)

	catalog := request(t, server.URL, http.MethodGet, "/v1/discovery-candidates", session, nil, nil)
	assertStatus(t, catalog, http.StatusOK)
	candidateItems := decodeObject(t, catalog)["items"].([]any)
	candidate := candidateItems[len(candidateItems)-1].(map[string]any)
	promotionPath := "/v1/discovery-candidates/" + candidate["id"].(string) + "/promotion"
	promotionHeaders := map[string]string{"Idempotency-Key": "promote-demo-1"}
	promotion := request(t, server.URL, http.MethodPost, promotionPath, session, promotionInput("promoted demo"), promotionHeaders)
	assertStatus(t, promotion, http.StatusCreated)
	firstPromotion := decodeObject(t, promotion)
	replayedPromotion := request(t, server.URL, http.MethodPost, promotionPath, session, promotionInput("promoted demo"), promotionHeaders)
	assertStatus(t, replayedPromotion, http.StatusCreated)
	secondPromotion := decodeObject(t, replayedPromotion)
	if firstPromotion["monitor"].(map[string]any)["id"] != secondPromotion["monitor"].(map[string]any)["id"] {
		t.Fatal("promotion idempotency changed the monitor ID")
	}
	tombstone := request(t, server.URL, http.MethodPost, "/v1/agent/discovery-candidates:batch", agentToken, map[string]any{
		"candidates": []map[string]any{{
			"sourceKind": "service", "sourceUid": "service/default/demo", "namespace": "default",
			"name": "demo", "labels": map[string]string{"namespace": "default"},
			"protocol": "http", "target": "https://demo.example.test/health",
			"networkPerspective": "cluster/default", "present": false,
			"observedAt": "2026-07-25T12:01:00Z",
		}},
		"complete": true, "completedAt": "2026-07-25T12:01:00Z",
	}, map[string]string{"Idempotency-Key": "discovery-demo-tombstone-1"})
	assertStatus(t, tombstone, http.StatusOK)
	stale := request(t, server.URL, http.MethodGet, "/v1/discovery-candidates/"+candidate["id"].(string), session, nil, nil)
	assertStatus(t, stale, http.StatusOK)
	staleCandidate := decodeObject(t, stale)
	if staleCandidate["present"] != false || staleCandidate["state"] != "promoted" ||
		staleCandidate["promotedMonitorId"] != firstPromotion["monitor"].(map[string]any)["id"] {
		t.Fatalf("tombstoned promoted candidate = %#v", staleCandidate)
	}

	channel := request(t, server.URL, http.MethodPost, "/v1/notification-channels", session, map[string]any{
		"name":    "mock chat",
		"enabled": true,
		"configuration": map[string]any{
			"kind":       "shoutrrr",
			"serviceUrl": "generic+https://secret.example.test",
		},
	}, nil)
	assertStatus(t, channel, http.StatusCreated)
	channelBody := string(readBody(t, channel))
	if strings.Contains(channelBody, "secret.example.test") || strings.Contains(channelBody, "serviceUrl") {
		t.Fatalf("notification response leaked configuration: %s", channelBody)
	}
	deliveries := request(t, server.URL, http.MethodGet, "/v1/notification-deliveries", session, nil, nil)
	assertStatus(t, deliveries, http.StatusOK)

	status := request(t, server.URL, http.MethodGet, "/v1/status-page", "", nil, nil)
	assertStatus(t, status, http.StatusOK)
	public := decodeObject(t, status)
	if public["state"] == "" || len(public["monitors"].([]any)) == 0 {
		t.Fatalf("public status = %#v", public)
	}
}

func TestStableFailureScenarioUsesRFC9457(t *testing.T) {
	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()

	response := request(
		t,
		server.URL,
		http.MethodGet,
		"/v1/status-page",
		"",
		nil,
		map[string]string{"X-Xisnove-Mock-Scenario": "conflict"},
	)
	assertProblem(t, response, http.StatusConflict, "mock_conflict")
	if response.Header.Get("Content-Type") != "application/problem+json" {
		t.Fatalf("Content-Type = %q", response.Header.Get("Content-Type"))
	}
}

func TestOpenAPIRequestValidationRejectsMissingRequiredFields(t *testing.T) {
	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()

	response := request(t, server.URL, http.MethodPost, "/v1/sessions", "", map[string]any{}, nil)
	assertProblem(t, response, http.StatusBadRequest, "validation_failed")
}

func TestOpenAPIRequestValidationRejectsUndeclaredBody(t *testing.T) {
	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()
	session := login(t, server.URL)

	response := request(
		t,
		server.URL,
		http.MethodPost,
		"/v1/agents/00000000-0000-4800-8000-000000000801/credential-rotations",
		session,
		map[string]any{},
		map[string]string{"Idempotency-Key": "unexpected-rotation-body-1"},
	)
	assertProblem(t, response, http.StatusBadRequest, "validation_failed")
}

func TestIdempotencyRejectsChangedRequestBody(t *testing.T) {
	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()
	session := login(t, server.URL)
	headers := map[string]string{"Idempotency-Key": "monitor-changed-body-1"}

	first := request(t, server.URL, http.MethodPost, "/v1/monitors", session, monitorInput("first"), headers)
	assertStatus(t, first, http.StatusCreated)
	_ = readBody(t, first)
	changed := request(t, server.URL, http.MethodPost, "/v1/monitors", session, monitorInput("changed"), headers)
	assertProblem(t, changed, http.StatusConflict, "idempotency_key_reused")
}

func TestCredentialIssuanceDoesNotReplayPlaintext(t *testing.T) {
	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()
	session := login(t, server.URL)
	headers := map[string]string{"Idempotency-Key": "token-plaintext-once-1"}
	body := map[string]any{"name": "automation", "scopes": []string{"monitors:read"}}

	created := request(t, server.URL, http.MethodPost, "/v1/api-tokens", session, body, headers)
	assertStatus(t, created, http.StatusCreated)
	plaintext := decodeObject(t, created)["token"].(string)
	replayed := request(t, server.URL, http.MethodPost, "/v1/api-tokens", session, body, headers)
	replayedBody := readBody(t, replayed)
	if replayed.StatusCode != http.StatusConflict || !strings.Contains(string(replayedBody), "credential_already_issued") {
		t.Fatalf("replay status=%d body=%s", replayed.StatusCode, replayedBody)
	}
	if strings.Contains(string(replayedBody), plaintext) {
		t.Fatalf("credential replay leaked plaintext %q", plaintext)
	}
}

func TestIdempotencyKeysAreScopedToTheAuthenticatedPrincipal(t *testing.T) {
	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()
	session := login(t, server.URL)
	createdToken := request(t, server.URL, http.MethodPost, "/v1/api-tokens", session, map[string]any{
		"name": "second writer", "scopes": []string{"monitors:write"},
	}, map[string]string{"Idempotency-Key": "second-writer-token-1"})
	assertStatus(t, createdToken, http.StatusCreated)
	secondPrincipal := decodeObject(t, createdToken)["token"].(string)
	headers := map[string]string{"Idempotency-Key": "principal-isolation-1"}

	first := request(t, server.URL, http.MethodPost, "/v1/monitors", mockapi.FixtureFullAPIToken, monitorInput("principal-one"), headers)
	assertStatus(t, first, http.StatusCreated)
	firstID := decodeObject(t, first)["id"]
	second := request(t, server.URL, http.MethodPost, "/v1/monitors", secondPrincipal, monitorInput("principal-two"), headers)
	assertStatus(t, second, http.StatusCreated)
	secondID := decodeObject(t, second)["id"]
	if firstID == secondID {
		t.Fatalf("principals shared idempotency result %v", firstID)
	}
}

func TestConcurrentIdempotentMutationsCreateOneResource(t *testing.T) {
	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()
	session := login(t, server.URL)
	headers := map[string]string{"Idempotency-Key": "concurrent-monitor-create-1"}

	const callers = 16
	start := make(chan struct{})
	ids := make(chan string, callers)
	errors := make(chan string, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			response := request(t, server.URL, http.MethodPost, "/v1/monitors", session, monitorInput("concurrent"), headers)
			if response.StatusCode != http.StatusCreated {
				errors <- string(readBody(t, response))
				return
			}
			ids <- decodeObject(t, response)["id"].(string)
		}()
	}
	close(start)
	group.Wait()
	close(ids)
	close(errors)
	for failure := range errors {
		t.Fatalf("concurrent mutation failed: %s", failure)
	}
	var first string
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatalf("idempotency produced IDs %s and %s", first, id)
		}
	}
}

func TestEveryAdvertisedOperationReachesTheStrictMockDispatcher(t *testing.T) {
	doc, err := mockapi.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}

	for path, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			operation := item.GetOperation(method)
			if operation == nil || operation.OperationID == "CreateSession" ||
				operation.OperationID == "RevokeAgentCredentialGeneration" {
				continue
			}
			path := replacePathParameters(path)
			if operation.OperationID == "SearchResources" {
				path += "?q=router"
			}
			if operation.OperationID == "PutOperatorAgentCredential" {
				path = strings.Replace(path, "/credentials/1", "/credentials/2", 1)
			}
			t.Run(operation.OperationID, func(t *testing.T) {
				server := httptest.NewServer(mockapi.NewServer().Handler())
				defer server.Close()
				session := login(t, server.URL)
				token := session
				if operation.OperationID == "HeartbeatAgent" || operation.OperationID == "LeaseAgentWork" ||
					operation.OperationID == "UpsertDiscoveryCandidates" || operation.OperationID == "UploadProbeResults" {
					token = agentToken
				}
				if operation.OperationID == "EnrollAgent" || operation.OperationID == "GetPublicStatusPage" {
					token = ""
				}
				body := advertisedOperationRequest(operation.OperationID)
				response := request(t, server.URL, strings.ToUpper(method), path, token, body, map[string]string{
					"Idempotency-Key": "advertised-operation-1",
				})
				defer response.Body.Close()
				if want := advertisedSuccessStatus(operation.OperationID); response.StatusCode != want {
					t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.StatusCode, want, readBody(t, response))
				}
			})
		}
	}
}

func TestAgentCredentialRotationLifecycleIsOverlapSafe(t *testing.T) {
	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()
	session := login(t, server.URL)

	rotate := func(key string) *http.Response {
		return request(t, server.URL, http.MethodPost,
			"/v1/agents/00000000-0000-4800-8000-000000000801/credential-rotations",
			session, nil, map[string]string{"Idempotency-Key": key})
	}
	heartbeat := func(token string, generation int) *http.Response {
		return request(t, server.URL, http.MethodPost, "/v1/agent/heartbeat", token, map[string]any{
			"version": "mock-test", "credentialGeneration": generation, "capabilities": []string{"http"},
		}, nil)
	}
	revoke := func(generation int) *http.Response {
		return request(t, server.URL, http.MethodDelete,
			fmt.Sprintf("/v1/agents/00000000-0000-4800-8000-000000000801/credentials/%d", generation),
			session, nil, nil)
	}
	assertStatus := func(response *http.Response, want int) {
		t.Helper()
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("status=%d want=%d body=%s", response.StatusCode, want, readBody(t, response))
		}
	}

	assertStatus(rotate("credential-lifecycle-rotate-1"), http.StatusCreated)
	assertStatus(rotate("credential-lifecycle-rotate-2"), http.StatusConflict)
	assertStatus(revoke(1), http.StatusConflict)
	assertStatus(revoke(2), http.StatusConflict)
	assertStatus(heartbeat(mockapi.FixtureAgentTokenGeneration2, 2), http.StatusNoContent)
	assertStatus(revoke(1), http.StatusNoContent)
	assertStatus(revoke(1), http.StatusNoContent)
	assertStatus(heartbeat(mockapi.FixtureAgentToken, 1), http.StatusUnauthorized)
	assertStatus(heartbeat(mockapi.FixtureAgentTokenGeneration2, 2), http.StatusNoContent)
	assertStatus(rotate("credential-lifecycle-rotate-3"), http.StatusCreated)
}

func TestOperatorMockReplaysOwnedApplyAndRejectsOwnershipAndHashConflicts(t *testing.T) {
	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()
	owner := map[string]any{"key": "monitoring.xisnove.io/Monitor/default/edge", "uid": "monitor-uid-1"}
	apply := map[string]any{"owner": owner, "monitor": updateMonitorInput("operator edge")}
	headers := map[string]string{"Idempotency-Key": "operator-monitor-apply-1"}
	first := request(t, server.URL, http.MethodPost, "/v1/operator/monitors:apply", mockapi.FixtureFullAPIToken, apply, headers)
	assertStatus(t, first, http.StatusOK)
	firstState := decodeObject(t, first)
	if firstState["externalId"] == "" || firstState["credential"] != nil {
		t.Fatalf("operator monitor result = %#v", firstState)
	}
	replayed := request(t, server.URL, http.MethodPost, "/v1/operator/monitors:apply", mockapi.FixtureFullAPIToken, apply, headers)
	assertStatus(t, replayed, http.StatusOK)
	if decodeObject(t, replayed)["externalId"] != firstState["externalId"] {
		t.Fatal("operator apply replay changed external ID")
	}
	deleteBody := map[string]any{
		"owner":      map[string]any{"key": owner["key"], "uid": "different-owner-uid"},
		"externalId": firstState["externalId"],
	}
	deleteResponse := request(t, server.URL, http.MethodPost, "/v1/operator/monitors:delete", mockapi.FixtureFullAPIToken, deleteBody, map[string]string{"Idempotency-Key": "operator-owner-conflict-1"})
	assertProblem(t, deleteResponse, http.StatusConflict, "operator_ownership_conflict")

	agentOwner := map[string]any{"key": "monitoring.xisnove.io/Agent/default/edge", "uid": "agent-uid-1"}
	agentApply := map[string]any{
		"owner": agentOwner, "name": "operator edge", "locationId": "00000000-0000-4000-8000-000000000001",
		"enabled": true, "capabilities": []string{"http"},
		"initialCredential": map[string]any{"generation": 1, "credential": "xisnove_mock_operator_initial_credential_0001"},
	}
	agentResponse := request(t, server.URL, http.MethodPost, "/v1/operator/agents:apply", mockapi.FixtureFullAPIToken, agentApply, map[string]string{"Idempotency-Key": "operator-agent-apply-1"})
	assertStatus(t, agentResponse, http.StatusOK)
	agentState := decodeObject(t, agentResponse)
	if agentState["externalId"] == "" || agentState["credential"] != nil {
		t.Fatalf("operator agent result = %#v", agentState)
	}
	credentialPath := "/v1/operator/agents/" + agentState["externalId"].(string) + "/credentials/2"
	rotation := map[string]any{"owner": agentOwner, "credential": "xisnove_mock_operator_rotation_credential_0002"}
	put := request(t, server.URL, http.MethodPut, credentialPath, mockapi.FixtureFullAPIToken, rotation, map[string]string{"Idempotency-Key": "operator-agent-put-2"})
	assertStatus(t, put, http.StatusNoContent)
	conflict := request(t, server.URL, http.MethodPut, credentialPath, mockapi.FixtureFullAPIToken,
		map[string]any{"owner": agentOwner, "credential": "xisnove_mock_operator_changed_credential_0002"},
		map[string]string{"Idempotency-Key": "operator-agent-put-changed-2"})
	assertProblem(t, conflict, http.StatusConflict, "operator_credential_hash_conflict")
}

func TestOperatorMockRejectsNonInitialAgentCredentialGeneration(t *testing.T) {
	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()
	response := request(t, server.URL, http.MethodPost, "/v1/operator/agents:apply", mockapi.FixtureFullAPIToken,
		map[string]any{
			"owner": map[string]any{"key": "monitoring.xisnove.io/Agent/default/edge", "uid": "agent-uid-generation-2"},
			"name":  "operator edge", "locationId": "00000000-0000-4000-8000-000000000001",
			"enabled": true, "capabilities": []string{"http"},
			"initialCredential": map[string]any{"generation": 2, "credential": "xisnove_mock_operator_initial_credential_0002"},
		}, map[string]string{"Idempotency-Key": "operator-agent-generation-2"})
	assertProblem(t, response, http.StatusBadRequest, "validation_failed")
}

func TestDiscoveryMockAcceptsAnEmptyCompleteSnapshot(t *testing.T) {
	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()
	response := request(t, server.URL, http.MethodPost, "/v1/agent/discovery-candidates:batch", agentToken,
		map[string]any{"candidates": []any{}, "complete": true, "completedAt": "2026-07-25T12:05:00Z"},
		map[string]string{"Idempotency-Key": "empty-complete-snapshot-1"})
	assertStatus(t, response, http.StatusOK)
	acknowledgement := decodeObject(t, response)
	if acknowledgement["accepted"] != float64(0) || acknowledgement["created"] != float64(0) || acknowledgement["updated"] != float64(0) {
		t.Fatalf("empty complete acknowledgement = %#v", acknowledgement)
	}
	catalog := request(t, server.URL, http.MethodGet, "/v1/discovery-candidates", mockapi.FixtureFullAPIToken, nil, nil)
	assertStatus(t, catalog, http.StatusOK)
	for _, item := range decodeObject(t, catalog)["items"].([]any) {
		if item.(map[string]any)["present"] != false {
			t.Fatalf("empty complete snapshot left candidate present: %#v", item)
		}
	}
}

func TestAgentResultUploadUsesContractAcknowledgementEnvelope(t *testing.T) {
	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()

	response := request(
		t,
		server.URL,
		http.MethodPost,
		"/v1/agent/results:batch",
		agentToken,
		map[string]any{"results": []any{probeResultInput()}},
		map[string]string{"Idempotency-Key": "empty-result-batch-1"},
	)
	assertStatus(t, response, http.StatusOK)
	body := decodeObject(t, response)
	if _, ok := body["acknowledgements"]; !ok {
		t.Fatalf("upload response = %#v, want acknowledgements", body)
	}
}

func TestNotificationReplayIsIdempotentAndHasNoResponseBody(t *testing.T) {
	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()
	session := login(t, server.URL)
	headers := map[string]string{"Idempotency-Key": "notification-replay-1"}
	path := "/v1/notification-deliveries/00000000-0000-4700-8000-000000000701/replay"

	for attempt := 1; attempt <= 2; attempt++ {
		response := request(t, server.URL, http.MethodPost, path, session, nil, headers)
		body := readBody(t, response)
		if response.StatusCode != http.StatusAccepted || len(body) != 0 {
			t.Fatalf("attempt %d status=%d body=%q", attempt, response.StatusCode, body)
		}
	}
}

func advertisedOperationRequest(operationID string) any {
	switch operationID {
	case "CreateAPIToken":
		return map[string]any{"name": "contract token", "scopes": []string{"monitors:read"}}
	case "UpdateAPIToken":
		return map[string]any{"name": "updated contract token"}
	case "CreateLocation", "UpdateLocation":
		return map[string]any{"name": "contract location"}
	case "CreateMonitor":
		return monitorInput("contract-monitor")
	case "UpdateMonitor":
		return updateMonitorInput("contract-monitor")
	case "CreateAgentEnrollmentToken":
		return map[string]any{
			"locationId": "00000000-0000-4000-8000-000000000001", "expiresInSeconds": 600,
		}
	case "EnrollAgent":
		return map[string]any{
			"token":      "xisnove_mock_enrollment_0000000000000000000001",
			"credential": agentToken, "name": "contract agent", "capabilities": []string{"http"},
		}
	case "HeartbeatAgent":
		return map[string]any{
			"version": "v0.4.0", "credentialGeneration": 1, "capabilities": []string{"http"},
		}
	case "LeaseAgentWork":
		return map[string]any{"waitSeconds": 0, "capabilities": []string{"http"}}
	case "UploadProbeResults":
		return map[string]any{"results": []any{probeResultInput()}}
	case "UpdateAgent":
		return map[string]any{"name": "updated contract agent"}
	case "UpsertDiscoveryCandidates":
		return map[string]any{"candidates": []any{map[string]any{
			"sourceKind": "service", "sourceUid": "service/default/contract", "namespace": "default",
			"name": "contract", "labels": map[string]string{}, "protocol": "http",
			"target": "https://contract.example.test/health", "networkPerspective": "cluster/default",
			"present":    true,
			"observedAt": "2026-07-25T12:00:00Z",
		}}, "complete": true, "completedAt": "2026-07-25T12:00:00Z"}
	case "ApplyOperatorMonitor":
		return map[string]any{"owner": operatorFixtureOwner(), "monitor": updateMonitorInput("operator contract monitor")}
	case "DeleteOperatorMonitor":
		return map[string]any{"owner": operatorFixtureOwner()}
	case "ObserveOperatorAgent":
		return map[string]any{"owner": operatorFixtureOwner()}
	case "ApplyOperatorAgent":
		return map[string]any{
			"owner": map[string]any{"key": "fixture/operator-agent-apply", "uid": "fixture-agent-apply-uid"}, "name": "operator contract agent",
			"locationId": "00000000-0000-4000-8000-000000000001", "enabled": true,
			"capabilities":      []string{"http"},
			"initialCredential": map[string]any{"generation": 1, "credential": "xisnove_mock_operator_contract_credential_01"},
		}
	case "PutOperatorAgentCredential":
		return map[string]any{"owner": operatorFixtureOwner(), "credential": "xisnove_mock_operator_fixture_credential_0002"}
	case "RevokeOperatorAgentCredential":
		return map[string]any{"owner": operatorFixtureOwner()}
	case "DeleteOperatorAgent":
		return map[string]any{"owner": operatorFixtureOwner()}
	case "PromoteDiscoveryCandidate":
		return promotionInput("contract promotion")
	case "CreateNotificationChannel", "UpdateNotificationChannel":
		return map[string]any{
			"name": "contract channel", "enabled": true,
			"configuration": map[string]any{
				"kind": "alertmanager", "endpoint": "https://alerts.example.test/api/v2/alerts",
			},
		}
	case "CreateNotificationRoute", "UpdateNotificationRoute":
		return map[string]any{
			"name": "contract route", "channelId": "00000000-0000-4500-8000-000000000501",
			"labelMatchers": map[string]string{}, "actions": []string{"open"},
			"severities": []string{"critical"}, "template": "{{ .MonitorName }} is {{ .State }}",
			"enabled": true, "precedence": 10,
		}
	case "CreateMaintenance":
		return map[string]any{
			"monitorId": "00000000-0000-4200-8000-000000000101",
			"startsAt":  "2026-07-25T13:00:00Z", "reason": "contract maintenance",
		}
	default:
		return nil
	}
}

func advertisedSuccessStatus(operationID string) int {
	switch operationID {
	case "RevokeCurrentSession", "RevokeAPIToken", "DisableLocation", "DisableMonitor",
		"HeartbeatAgent", "LeaseAgentWork", "RevokeAgent", "RevokeAgentCredentialGeneration", "DisableNotificationChannel",
		"DisableNotificationRoute", "DeleteMaintenance", "DeleteOperatorMonitor", "PutOperatorAgentCredential",
		"RevokeOperatorAgentCredential", "DeleteOperatorAgent":
		return http.StatusNoContent
	case "CreateAPIToken", "CreateLocation", "CreateMonitor", "CreateAgentEnrollmentToken",
		"EnrollAgent", "RotateAgentCredential", "PromoteDiscoveryCandidate",
		"CreateNotificationChannel", "CreateNotificationRoute", "CreateMaintenance":
		return http.StatusCreated
	case "ReplayNotificationDelivery":
		return http.StatusAccepted
	default:
		return http.StatusOK
	}
}

func operatorFixtureOwner() map[string]any {
	return map[string]any{"key": "fixture/operator-agent", "uid": "fixture-agent-uid"}
}

func replacePathParameters(path string) string {
	fixtures := map[string]string{
		"{tokenId}":       "00000000-0000-4100-8000-000000000001",
		"{locationId}":    "00000000-0000-4000-8000-000000000001",
		"{monitorId}":     "00000000-0000-4200-8000-000000000101",
		"{agentId}":       "00000000-0000-4800-8000-000000000801",
		"{generation}":    "1",
		"{incidentId}":    "00000000-0000-4300-8000-000000000201",
		"{candidateId}":   "00000000-0000-4400-8000-000000000401",
		"{channelId}":     "00000000-0000-4500-8000-000000000501",
		"{routeId}":       "00000000-0000-4600-8000-000000000601",
		"{deliveryId}":    "00000000-0000-4700-8000-000000000701",
		"{maintenanceId}": "00000000-0000-4900-8000-000000000901",
	}
	for parameter, value := range fixtures {
		path = strings.ReplaceAll(path, parameter, value)
	}
	return path
}

func login(t *testing.T, baseURL string) string {
	t.Helper()
	response := request(t, baseURL, http.MethodPost, "/v1/sessions", "", map[string]any{
		"email": adminEmail, "password": adminPassword,
	}, nil)
	assertStatus(t, response, http.StatusCreated)
	return decodeObject(t, response)["token"].(string)
}

func monitorInput(name string) map[string]any {
	return map[string]any{
		"name": name, "intervalSeconds": 30, "timeoutMillis": 5000,
		"failureThreshold": 3, "recoveryThreshold": 2,
		"locationId":       "00000000-0000-4000-8000-000000000001",
		"requiredLocation": true,
		"probe": map[string]any{
			"kind": "http", "method": "GET",
			"url":     "https://" + name + ".example.test/health",
			"headers": map[string]string{}, "body": "",
			"expectedStatus": []map[string]int{{"minimum": 200, "maximum": 299}},
			"bodyContains":   []string{}, "bodyDoesNotContain": []string{},
			"followRedirects": false,
		},
	}
}

func updateMonitorInput(name string) map[string]any {
	input := monitorInput(name)
	input["description"] = "contract monitor"
	input["labels"] = map[string]string{}
	input["displayOrder"] = 0
	input["public"] = false
	input["enabled"] = true
	return input
}

func promotionInput(name string) map[string]any {
	return map[string]any{
		"name": name, "intervalSeconds": 30, "timeoutMillis": 5000,
		"failureThreshold": 3, "recoveryThreshold": 2,
		"locationId":       "00000000-0000-4000-8000-000000000001",
		"requiredLocation": true, "public": true,
	}
}

func probeResultInput() map[string]any {
	return map[string]any{
		"resultId":   "00000000-0000-4a00-8000-000000000001",
		"runId":      "00000000-0000-4a00-8000-000000000002",
		"leaseToken": "xisnove_mock_lease_000000000000000000000001",
		"startedAt":  "2026-07-25T12:00:00Z", "finishedAt": "2026-07-25T12:00:01Z",
		"outcome": "passed", "latencyMillis": 100, "observedStatus": 200,
		"bodyAssertionPassed": true, "errorCode": "", "diagnosticSample": "ok",
	}
}

func request(
	t *testing.T,
	baseURL, method, path, token string,
	body any,
	headers map[string]string,
) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, baseURL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func assertStatus(t *testing.T, response *http.Response, want int) {
	t.Helper()
	if response.StatusCode != want {
		t.Fatalf("status = %d, want %d, body = %s", response.StatusCode, want, readBody(t, response))
	}
}

func assertProblem(t *testing.T, response *http.Response, status int, code string) {
	t.Helper()
	if response.StatusCode != status {
		t.Fatalf("status = %d, want %d, body = %s", response.StatusCode, status, readBody(t, response))
	}
	problem := decodeObject(t, response)
	if problem["code"] != code || problem["type"] == "" ||
		problem["title"] == "" || problem["correlationId"] == "" {
		t.Fatalf("problem = %#v", problem)
	}
}

func decodeObject(t *testing.T, response *http.Response) map[string]any {
	t.Helper()
	defer response.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func readBody(t *testing.T, response *http.Response) []byte {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	return body
}
