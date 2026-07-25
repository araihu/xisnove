package sdk_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/araihu/xisnove/sdk"
)

const monitorID = "5d85f8f1-e010-4bc4-8843-3b33b67f945f"

func TestRequireMonitorReturnsDecodedMonitor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/monitors/"+monitorID {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id":"5d85f8f1-e010-4bc4-8843-3b33b67f945f",
			"kind":"http",
			"name":"router",
			"intervalSeconds":30,
			"timeoutMillis":5000,
			"failureThreshold":2,
			"recoveryThreshold":1,
			"locationId":"0f9d676e-b04e-4a21-9353-7b70317a38ed",
			"requiredLocation":true,
			"probe":{
				"kind":"http",
				"method":"GET",
				"url":"https://router.example.test/health",
				"headers":{},
				"body":"",
				"expectedStatus":[{"minimum":200,"maximum":299}],
				"bodyContains":["ok"],
				"bodyDoesNotContain":[],
				"followRedirects":false
			},
			"createdAt":"2026-07-25T00:00:00Z",
			"updatedAt":"2026-07-25T00:00:00Z"
		}`))
	}))
	defer server.Close()

	client, err := sdk.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	monitor, err := client.RequireMonitor(context.Background(), monitorID)
	if err != nil {
		t.Fatal(err)
	}
	if monitor.Name != "router" {
		t.Fatalf("Name = %q", monitor.Name)
	}
	if monitor.Id.String() != monitorID {
		t.Fatalf("Id = %q", monitor.Id)
	}
	probe, err := monitor.Probe.AsHTTPProbeDefinition()
	if err != nil || probe.Url != "https://router.example.test/health" {
		t.Fatalf("Probe = %#v, error = %v", probe, err)
	}
}

func TestRequireMonitorIncludesUnexpectedHTTPStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{
			"type":"https://xisnove.dev/problems/not-found",
			"title":"Monitor not found",
			"status":404,
			"code":"monitor_not_found",
			"correlationId":"request-1"
		}`))
	}))
	defer server.Close()

	client, err := sdk.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.RequireMonitor(context.Background(), monitorID)
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("error = %v", err)
	}
}
