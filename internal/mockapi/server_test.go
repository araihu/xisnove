package mockapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
		"scopes": []string{"management:read"},
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
		"scopes": []string{"management:read", "management:write"},
	}, map[string]string{"Idempotency-Key": "token-ui-write-1"})
	assertStatus(t, response, http.StatusOK)
	response = request(t, server.URL, http.MethodPost, "/v1/monitors", token, monitorInput("allowed"), nil)
	assertStatus(t, response, http.StatusCreated)

	response = request(t, server.URL, http.MethodDelete, "/v1/api-tokens/"+tokenID, session, nil, nil)
	assertStatus(t, response, http.StatusNoContent)
	response = request(t, server.URL, http.MethodGet, "/v1/monitors", token, nil, nil)
	assertProblem(t, response, http.StatusUnauthorized, "unauthorized")
}

func TestMonitorsAreCursorPagedAndIdempotent(t *testing.T) {
	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()
	session := login(t, server.URL)

	first := request(t, server.URL, http.MethodGet, "/v1/monitors?limit=1", session, nil, nil)
	assertStatus(t, first, http.StatusOK)
	firstPage := decodeObject(t, first)
	if len(firstPage["items"].([]any)) != 1 || firstPage["nextCursor"] == "" {
		t.Fatalf("first page = %#v", firstPage)
	}
	cursor := firstPage["nextCursor"].(string)
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
			"externalId": "service/default/demo",
			"kind":       "http",
			"name":       "demo",
			"target":     "https://demo.example.test/health",
			"labels":     map[string]string{"namespace": "default"},
			"observedAt": "2026-07-25T12:00:00Z",
		}},
	}, map[string]string{"Idempotency-Key": "discovery-demo-1"})
	assertStatus(t, batch, http.StatusOK)

	catalog := request(t, server.URL, http.MethodGet, "/v1/discovery-candidates", session, nil, nil)
	assertStatus(t, catalog, http.StatusOK)
	candidateItems := decodeObject(t, catalog)["items"].([]any)
	candidate := candidateItems[len(candidateItems)-1].(map[string]any)
	promotionPath := "/v1/discovery-candidates/" + candidate["id"].(string) + ":promote"
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

	status := request(t, server.URL, http.MethodGet, "/v1/status", "", nil, nil)
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
		"/v1/status",
		"",
		nil,
		map[string]string{"X-Xisnove-Mock-Scenario": "conflict"},
	)
	assertProblem(t, response, http.StatusConflict, "mock_conflict")
	if response.Header.Get("Content-Type") != "application/problem+json" {
		t.Fatalf("Content-Type = %q", response.Header.Get("Content-Type"))
	}
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

func promotionInput(name string) map[string]any {
	return map[string]any{
		"name": name, "intervalSeconds": 30, "timeoutMillis": 5000,
		"failureThreshold": 3, "recoveryThreshold": 2,
		"locationId":       "00000000-0000-4000-8000-000000000001",
		"requiredLocation": true, "public": true,
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
