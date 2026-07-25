package worker_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/agent/internal/controlplane"
	"github.com/araihu/xisnove/agent/worker"
)

func TestRunOnceLeasesExecutesAndUploadsResult(t *testing.T) {
	for _, acknowledgement := range []string{"accepted", "duplicate"} {
		t.Run(acknowledgement, func(t *testing.T) {
			work := testHTTPWork()
			var uploaded controlplane.ProbeResultBatch
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
				Client: client,
				Credential: func() (string, error) {
					return "agent-credential", nil
				},
				Executor:             fixedExecutor{},
				Version:              "v0.1.0",
				CredentialGeneration: 1,
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
		Client: client,
		Credential: func() (string, error) {
			return "agent-credential", nil
		},
		Executor:             fixedExecutor{},
		Version:              "v0.1.0",
		CredentialGeneration: 1,
	}

	if err := instance.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type fixedExecutor struct{}

func (fixedExecutor) Execute(
	_ context.Context,
	_ controlplane.HTTPWork,
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

func testHTTPWork() controlplane.HTTPWork {
	return controlplane.HTTPWork{
		RunId:         uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		MonitorId:     uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		LeaseToken:    "lease-token",
		ScheduledFor:  time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC),
		TimeoutMillis: 5000,
		Http: controlplane.HTTPProbe{
			Method:         controlplane.GET,
			Url:            "https://example.com/health",
			ExpectedStatus: 200,
		},
	}
}
