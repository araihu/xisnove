package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/araihu/xisnove/internal/mockapi"
	monitoringv1alpha1 "github.com/araihu/xisnove/operator/api/v1alpha1"
	"github.com/araihu/xisnove/operator/internal/controlplane"
)

const (
	fixtureLocationID = "00000000-0000-4000-8000-000000000001"
	fixtureAgentID    = "00000000-0000-4800-8000-000000000801"
)

func TestApplyAgentUsesBearerIdempotencyAndDistinctOwnerIdentity(t *testing.T) {
	t.Parallel()

	mock := mockapi.NewServer()
	server := httptest.NewServer(assertRequest(t, mock.Handler(), func(r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Bearer "+mockapi.FixtureFullAPIToken; got != want {
			t.Errorf("Authorization = %q, want bearer provisioning token", got)
		}
		if got, want := r.Header.Get("Idempotency-Key"), "agent/uid-1/apply/1"; got != want {
			t.Errorf("Idempotency-Key = %q, want %q", got, want)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var received struct {
			Owner struct {
				Key string `json:"key"`
				UID string `json:"uid"`
			} `json:"owner"`
		}
		if err := json.Unmarshal(body, &received); err != nil {
			t.Fatal(err)
		}
		if received.Owner.Key != "monitoring.xisnove.io/Agent/default/edge" || received.Owner.UID != "uid-1" {
			t.Errorf("owner = %#v, want separate key and UID", received.Owner)
		}
	}))
	defer server.Close()

	client := newClient(t, server.URL)
	state, err := client.ApplyAgent(context.Background(), controlplane.ApplyAgentRequest{
		Owner: controlplane.OwnerReference{Key: "monitoring.xisnove.io/Agent/default/edge", UID: "uid-1"},
		Name:  "edge",
		Spec: monitoringv1alpha1.AgentSpec{
			LocationID:   fixtureLocationID,
			Capabilities: []monitoringv1alpha1.AgentCapability{monitoringv1alpha1.AgentCapabilityHTTP},
		},
		InitialCredential: []byte("operator-credential-material-00000001"),
		IdempotencyKey:    "agent/uid-1/apply/1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := state.ExternalID, "00000000-0000-4800-8000-000000000001"; got != want {
		t.Fatalf("ExternalID = %q, want %q", got, want)
	}
	if got, want := state.CredentialGeneration, int64(1); got != want {
		t.Fatalf("CredentialGeneration = %d, want %d", got, want)
	}
}

func TestCredentialRotationAndRevokeUseTheMockOwnershipContract(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()
	client := newClient(t, server.URL)
	owner := controlplane.OwnerReference{Key: "fixture/operator-agent", UID: "fixture-agent-uid"}

	if err := client.PutAgentCredential(context.Background(), controlplane.PutAgentCredentialRequest{
		Owner:          owner,
		ExternalID:     fixtureAgentID,
		Generation:     3,
		Credential:     []byte("operator-credential-material-00000003"),
		IdempotencyKey: "agent/fixture/put/3",
	}); err != nil {
		t.Fatal(err)
	}
	if err := client.RevokeAgentCredential(context.Background(), controlplane.RevokeAgentCredentialRequest{
		Owner:          owner,
		ExternalID:     fixtureAgentID,
		Generation:     1,
		IdempotencyKey: "agent/fixture/revoke/1",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteMonitorAllowsAnEmptyExternalID(t *testing.T) {
	t.Parallel()

	mock := mockapi.NewServer()
	server := httptest.NewServer(assertRequest(t, mock.Handler(), func(r *http.Request) {
		if r.URL.Path != "/v1/operator/monitors:apply" {
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var received struct {
			Monitor struct {
				Enabled bool `json:"enabled"`
			} `json:"monitor"`
		}
		if err := json.Unmarshal(body, &received); err != nil {
			t.Fatal(err)
		}
		if !received.Monitor.Enabled {
			t.Error("applied monitor was disabled")
		}
	}))
	defer server.Close()
	client := newClient(t, server.URL)
	owner := controlplane.OwnerReference{Key: "monitoring.xisnove.io/Monitor/default/router", UID: "uid-router"}

	if _, err := client.ApplyMonitor(context.Background(), controlplane.ApplyMonitorRequest{
		Owner:          owner,
		Name:           "router",
		Spec:           httpMonitorSpec(),
		IdempotencyKey: "monitor/uid-router/apply",
	}); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteMonitor(context.Background(), controlplane.DeleteRemoteObjectRequest{
		Owner:          owner,
		IdempotencyKey: "monitor/uid-router/delete",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteAgentAllowsAnEmptyExternalID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(mockapi.NewServer().Handler())
	defer server.Close()
	client := newClient(t, server.URL)
	owner := controlplane.OwnerReference{Key: "fixture/operator-agent", UID: "fixture-agent-uid"}
	if err := client.DeleteAgent(context.Background(), controlplane.DeleteRemoteObjectRequest{
		Owner:          owner,
		IdempotencyKey: "agent/fixture/delete",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMapsNotFoundAndOwnershipAndCredentialHashConflicts(t *testing.T) {
	t.Parallel()

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(mockapi.NewServer().Handler())
		defer server.Close()
		client, err := New(server.URL, mockapi.FixtureFullAPIToken, WithHTTPClient(&http.Client{Transport: scenarioTransport{scenario: "not-found"}}))
		if err != nil {
			t.Fatal(err)
		}
		err = client.DeleteAgent(context.Background(), controlplane.DeleteRemoteObjectRequest{
			Owner:          controlplane.OwnerReference{Key: "missing", UID: "uid"},
			IdempotencyKey: "agent/missing/delete",
		})
		if !errors.Is(err, controlplane.ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
	})

	t.Run("ownership", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(mockapi.NewServer().Handler())
		defer server.Close()
		client := newClient(t, server.URL)
		err := client.PutAgentCredential(context.Background(), controlplane.PutAgentCredentialRequest{
			Owner:          controlplane.OwnerReference{Key: "other", UID: "uid"},
			ExternalID:     fixtureAgentID,
			Generation:     3,
			Credential:     []byte("operator-credential-material-00000003"),
			IdempotencyKey: "agent/other/put/3",
		})
		if !errors.Is(err, controlplane.ErrOwnershipConflict) {
			t.Fatalf("error = %v, want ErrOwnershipConflict", err)
		}
	})

	t.Run("credential hash", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(mockapi.NewServer().Handler())
		defer server.Close()
		client := newClient(t, server.URL)
		err := client.PutAgentCredential(context.Background(), controlplane.PutAgentCredentialRequest{
			Owner:          controlplane.OwnerReference{Key: "fixture/operator-agent", UID: "fixture-agent-uid"},
			ExternalID:     fixtureAgentID,
			Generation:     2,
			Credential:     []byte("operator-credential-material-00000002"),
			IdempotencyKey: "agent/fixture/put/2-conflict",
		})
		if !errors.Is(err, controlplane.ErrCredentialConflict) {
			t.Fatalf("error = %v, want ErrCredentialConflict", err)
		}
	})
}

func TestFailureDiagnosticsAreRedactedAndBodiesAreClosed(t *testing.T) {
	t.Parallel()

	for _, fixture := range []struct {
		name        string
		contentType string
		body        string
	}{
		{
			name:        "non JSON",
			contentType: "text/plain",
			body:        "upstream diagnostic credential=operator-credential-material-00000004",
		},
		{
			name:        "RFC 9457",
			contentType: "application/problem+json",
			body:        `{"type":"https://example.test/problems/validation","title":"invalid","status":422,"code":"validation_failed","correlationId":"request-1","detail":"credential=operator-credential-material-00000004"}`,
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			body := &closeTrackingBody{Reader: bytes.NewBufferString(fixture.body)}
			client, err := New("https://control-plane.example.test", mockapi.FixtureFullAPIToken, WithHTTPClient(staticDoer{
				response: &http.Response{
					StatusCode: http.StatusUnprocessableEntity,
					Status:     "422 Unprocessable Entity",
					Header:     http.Header{"Content-Type": []string{fixture.contentType}},
					Body:       body,
				},
			}))
			if err != nil {
				t.Fatal(err)
			}
			err = client.DeleteAgent(context.Background(), controlplane.DeleteRemoteObjectRequest{
				Owner:          controlplane.OwnerReference{Key: "owner", UID: "uid"},
				ExternalID:     fixtureAgentID,
				IdempotencyKey: "agent/failure/delete",
			})
			if err == nil {
				t.Fatal("DeleteAgent succeeded")
			}
			if got := err.Error(); len(got) > 128 || bytes.Contains([]byte(got), []byte("operator-credential-material")) {
				t.Fatalf("error leaked unbounded diagnostics: %q", got)
			}
			if !body.closed {
				t.Fatal("response body was not closed")
			}
		})
	}
}

func TestTransportErrorsDoNotRevealCredentials(t *testing.T) {
	t.Parallel()

	client, err := New("https://control-plane.example.test", mockapi.FixtureFullAPIToken, WithHTTPClient(failingDoer{
		err: errors.New("dial failed for credential=operator-credential-material-00000005"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	err = client.DeleteAgent(context.Background(), controlplane.DeleteRemoteObjectRequest{
		Owner:          controlplane.OwnerReference{Key: "owner", UID: "uid"},
		ExternalID:     fixtureAgentID,
		IdempotencyKey: "agent/transport/delete",
	})
	if err == nil {
		t.Fatal("DeleteAgent succeeded")
	}
	if got := err.Error(); bytes.Contains([]byte(got), []byte("operator-credential-material")) {
		t.Fatalf("error leaked credentials: %q", got)
	}
}

func newClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	client, err := New(baseURL, mockapi.FixtureFullAPIToken)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func assertRequest(t *testing.T, next http.Handler, check func(*http.Request)) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		check(r)
		next.ServeHTTP(w, r)
	})
}

type scenarioTransport struct{ scenario string }

func (transport scenarioTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("X-Xisnove-Mock-Scenario", transport.scenario)
	return http.DefaultTransport.RoundTrip(request)
}

type staticDoer struct{ response *http.Response }

func (doer staticDoer) Do(*http.Request) (*http.Response, error) { return doer.response, nil }

type failingDoer struct{ err error }

func (doer failingDoer) Do(*http.Request) (*http.Response, error) { return nil, doer.err }

type closeTrackingBody struct {
	io.Reader
	closed bool
}

func (body *closeTrackingBody) Close() error {
	body.closed = true
	return nil
}

func httpMonitorSpec() monitoringv1alpha1.MonitorSpec {
	return monitoringv1alpha1.MonitorSpec{
		LocationID:        fixtureLocationID,
		IntervalSeconds:   30,
		TimeoutMillis:     5000,
		FailureThreshold:  2,
		RecoveryThreshold: 1,
		Probe: monitoringv1alpha1.MonitorProbeSpec{
			Kind: "http",
			HTTP: &monitoringv1alpha1.HTTPProbeSpec{
				URL:            "https://router.example.test/health",
				ExpectedStatus: []monitoringv1alpha1.StatusRange{{Minimum: 200, Maximum: 299}},
			},
		},
	}
}
