package worker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/agent/credentials"
	"github.com/araihu/xisnove/agent/internal/controlplane"
	"github.com/araihu/xisnove/agent/worker"
)

func TestRunOnceReloadsCredentialBundleBetweenHeartbeats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.json")
	writeCredentialBundle(t, path, `{"credential":"first-credential","generation":7}`)
	var heartbeats []struct {
		credential string
		generation int64
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/agent/heartbeat":
			var heartbeat controlplane.AgentHeartbeat
			if err := json.NewDecoder(request.Body).Decode(&heartbeat); err != nil {
				t.Fatal(err)
			}
			heartbeats = append(heartbeats, struct {
				credential string
				generation int64
			}{request.Header.Get("Authorization"), heartbeat.CredentialGeneration})
			response.WriteHeader(http.StatusNoContent)
		case "/v1/agent/work:lease":
			response.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	client, err := controlplane.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	instance := worker.Worker{
		Client:      client,
		Credentials: credentials.FileProvider{Path: path},
		Executor:    fixedExecutor{},
		Version:     "v0.1.0",
	}
	if err := instance.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(filepath.Dir(path), "replacement.json")
	writeCredentialBundle(t, replacement, `{"credential":"second-credential","generation":8}`)
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if err := instance.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []struct {
		credential string
		generation int64
	}{
		{"Bearer first-credential", 7},
		{"Bearer second-credential", 8},
	}
	if len(heartbeats) != len(want) {
		t.Fatalf("heartbeats = %#v", heartbeats)
	}
	for index := range want {
		if heartbeats[index] != want[index] {
			t.Fatalf("heartbeat[%d] = %#v, want %#v", index, heartbeats[index], want[index])
		}
	}
}

func TestRunOnceLeasesExecutesAndUploadsResult(t *testing.T) {
	for _, acknowledgement := range []string{"accepted", "duplicate"} {
		t.Run(acknowledgement, func(t *testing.T) {
			work := testHTTPWork()
			var uploaded controlplane.ProbeResultBatch
			leaseDelivered := false
			server := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer agent-credential" {
						http.Error(w, "missing bearer credential", http.StatusUnauthorized)
						return
					}
					switch r.URL.Path {
					case "/v1/agent/heartbeat":
						w.WriteHeader(http.StatusNoContent)
					case "/v1/agent/work:lease":
						if leaseDelivered {
							w.WriteHeader(http.StatusNoContent)
							return
						}
						leaseDelivered = true
						w.Header().Set("Content-Type", "application/json")
						_ = json.NewEncoder(w).Encode(work)
					case "/v1/agent/results:batch":
						if err := json.NewDecoder(r.Body).Decode(&uploaded); err != nil {
							t.Error(err)
							http.Error(w, "invalid batch", http.StatusBadRequest)
							return
						}
						w.Header().Set("Content-Type", "application/json")
						_ = json.NewEncoder(w).Encode(map[string]any{
							"acknowledgements": []map[string]any{{
								"resultId": uploaded.Results[0].ResultId,
								"status":   acknowledgement,
							}},
						})
					default:
						http.NotFound(w, r)
					}
				},
			))
			defer server.Close()
			client, err := controlplane.NewClientWithResponses(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			instance := worker.Worker{
				Client:      client,
				Credentials: fixedCredentials{bundle: credentials.Bundle{Credential: "agent-credential", Generation: 11}},
				Executor:    fixedExecutor{},
				Version:     "v0.1.0",
			}

			if err := instance.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(uploaded.Results) != 1 {
				t.Fatalf("results = %d", len(uploaded.Results))
			}
			result := uploaded.Results[0]
			if result.ResultId == uuid.Nil ||
				result.RunId != work.RunId ||
				result.LeaseToken != work.LeaseToken {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestRunOnceReturnsNilWhenLeaseHasNoWork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/agent/heartbeat", "/v1/agent/work:lease":
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Fatalf("unexpected path %s", r.URL.Path)
			}
		},
	))
	defer server.Close()
	client, err := controlplane.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	instance := worker.Worker{
		Client:      client,
		Credentials: fixedCredentials{bundle: credentials.Bundle{Credential: "agent-credential", Generation: 12}},
		Executor:    fixedExecutor{},
		Version:     "v0.1.0",
	}

	if err := instance.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRunOnceAdvertisesDiscoveryButLeasesOnlyProbeCapabilities(t *testing.T) {
	var heartbeat controlplane.AgentHeartbeat
	var lease controlplane.LeaseWorkRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agent/heartbeat":
			if err := json.NewDecoder(r.Body).Decode(&heartbeat); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusNoContent)
		case "/v1/agent/work:lease":
			if err := json.NewDecoder(r.Body).Decode(&lease); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	client, err := controlplane.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := []controlplane.AgentCapability{
		controlplane.AgentCapabilityHttp,
		controlplane.AgentCapabilityKubernetesDiscovery,
		controlplane.AgentCapabilityKubernetesWatch,
	}
	instance := worker.Worker{Client: client, Credentials: fixedCredentials{bundle: credentials.Bundle{Credential: "credential", Generation: 13}}, Executor: fixedExecutor{}, Capabilities: capabilities}
	if err := instance.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(heartbeat.Capabilities) != 3 {
		t.Fatalf("heartbeat capabilities=%v", heartbeat.Capabilities)
	}
	if len(lease.Capabilities) != 1 || lease.Capabilities[0] != controlplane.AgentCapabilityHttp {
		t.Fatalf("lease capabilities=%v", lease.Capabilities)
	}
}

func TestRunOnceDiscoveryOnlyHeartbeatDoesNotRequestProbeLease(t *testing.T) {
	leaseCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agent/heartbeat":
			w.WriteHeader(http.StatusNoContent)
		case "/v1/agent/work:lease":
			leaseCalls++
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	client, err := controlplane.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	instance := worker.Worker{Client: client, Credentials: fixedCredentials{bundle: credentials.Bundle{Credential: "credential", Generation: 14}}, Executor: fixedExecutor{}, Capabilities: []controlplane.AgentCapability{controlplane.AgentCapabilityKubernetesDiscovery}}
	if err := instance.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if leaseCalls != 0 {
		t.Fatalf("lease calls=%d", leaseCalls)
	}
}

func TestRunOnceBatchUploadRetriesIdenticalResults(t *testing.T) {
	works := []controlplane.ProbeWork{testHTTPWork(), testHTTPWork(), testHTTPWork()}
	for index := range works {
		works[index].RunId = uuid.New()
		works[index].LeaseToken = "lease-token-" + string(rune('a'+index))
	}
	leaseIndex := 0
	var uploads [][]byte
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/v1/agent/heartbeat":
				w.WriteHeader(http.StatusNoContent)
			case "/v1/agent/work:lease":
				if leaseIndex == len(works) {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(works[leaseIndex])
				leaseIndex++
			case "/v1/agent/results:batch":
				body, _ := io.ReadAll(request.Body)
				uploads = append(uploads, body)
				if len(uploads) == 1 {
					http.Error(w, "retry", http.StatusServiceUnavailable)
					return
				}
				var batch controlplane.ProbeResultBatch
				if err := json.Unmarshal(body, &batch); err != nil {
					t.Fatal(err)
				}
				acknowledgements := make([]map[string]any, len(batch.Results))
				for index, result := range batch.Results {
					status := "accepted"
					if index == 1 {
						status = "duplicate"
					}
					acknowledgements[index] = map[string]any{
						"resultId": result.ResultId, "status": status,
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"acknowledgements": acknowledgements,
				})
			}
		},
	))
	t.Cleanup(server.Close)
	client, err := controlplane.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	instance := worker.Worker{
		Client: client, Credentials: fixedCredentials{bundle: credentials.Bundle{Credential: "credential", Generation: 15}},
		Executor: fixedExecutor{}, Version: "test",
	}
	if err := instance.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(uploads) != 2 || !bytes.Equal(uploads[0], uploads[1]) {
		t.Fatalf("uploads = %d identical=%v", len(uploads), len(uploads) == 2 &&
			bytes.Equal(uploads[0], uploads[1]))
	}
	var batch controlplane.ProbeResultBatch
	if err := json.Unmarshal(uploads[1], &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch.Results) != 3 {
		t.Fatalf("results = %d", len(batch.Results))
	}
}

type fixedExecutor struct{}

func (fixedExecutor) Execute(
	_ context.Context,
	_ controlplane.ProbeWork,
) controlplane.ProbeResultInput {
	now := time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC)
	return controlplane.ProbeResultInput{
		StartedAt:           now,
		FinishedAt:          now.Add(time.Second),
		Outcome:             controlplane.Passed,
		ObservedStatus:      200,
		BodyAssertionPassed: true,
		ErrorCode:           controlplane.Empty,
	}
}

func testHTTPWork() controlplane.ProbeWork {
	var definition controlplane.ProbeDefinition
	if err := definition.FromHTTPProbeDefinition(controlplane.HTTPProbeDefinition{
		Method: controlplane.GET, Url: "https://example.com/health",
		Headers: map[string]string{}, Body: []byte{},
		ExpectedStatus: []controlplane.StatusRange{{
			Minimum: 200, Maximum: 200,
		}},
		BodyContains: []string{}, BodyDoesNotContain: []string{},
	}); err != nil {
		panic(err)
	}
	return controlplane.ProbeWork{
		RunId:         uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		MonitorId:     uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		LeaseToken:    "lease-token",
		ScheduledFor:  time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC),
		TimeoutMillis: 5000,
		Probe:         definition,
	}
}

func writeCredentialBundle(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

type fixedCredentials struct {
	bundle credentials.Bundle
	err    error
}

func (credentials fixedCredentials) Current(context.Context) (credentials.Bundle, error) {
	return credentials.bundle, credentials.err
}
