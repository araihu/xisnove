package alertmanager_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
	alertmanageradapter "github.com/araihu/xisnove/internal/adapters/alertmanager"
)

func TestTransportPostsFiringAndResolvedAlertsWithStableLabels(t *testing.T) {
	var requests []capturedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		requests = append(requests, capturedRequest{Path: request.URL.Path, Authorization: request.Header.Get("Authorization"), Body: body})
		w.Header().Set("X-Request-ID", "request-42")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	transport := newTransport(t, server.Client(), time.Second, 2)

	openedAt := time.Date(2026, time.July, 25, 20, 30, 0, 123, time.FixedZone("test", -3*60*60))
	firing := delivery(t, server.URL, "top-secret", domain.NotificationOpen, openedAt)
	result := transport.Send(context.Background(), firing)
	if result.Outcome != application.TransportDelivered || result.ProviderReceipt != "request-42" {
		t.Fatalf("firing Send() = %#v", result)
	}
	recovered := firing
	recovered.DeliveryID = "delivery-recover"
	recovered.Snapshot.Action = domain.NotificationRecover
	recovered.Snapshot.State = domain.HealthUp
	recovered.Snapshot.OccurredAt = openedAt.Add(5 * time.Minute)
	result = transport.Send(context.Background(), recovered)
	if result.Outcome != application.TransportDelivered {
		t.Fatalf("resolved Send() = %#v", result)
	}
	if len(requests) != 2 || requests[0].Path != "/api/v2/alerts" || requests[0].Authorization != "Bearer top-secret" {
		t.Fatalf("requests = %#v", requests)
	}
	first := decodeAlert(t, requests[0].Body)
	second := decodeAlert(t, requests[1].Body)
	if first.Labels["xisnove_fingerprint"] == "" || first.Labels["xisnove_fingerprint"] != second.Labels["xisnove_fingerprint"] {
		t.Fatalf("fingerprints = %q, %q", first.Labels["xisnove_fingerprint"], second.Labels["xisnove_fingerprint"])
	}
	if first.EndsAt != "" || second.EndsAt != recovered.Snapshot.OccurredAt.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("end times = %q, %q", first.EndsAt, second.EndsAt)
	}
	if first.StartsAt != openedAt.UTC().Format(time.RFC3339Nano) || first.Annotations["description"] != firing.Message {
		t.Fatalf("firing alert = %#v", first)
	}
}

func TestTransportClassifiesProviderAndTransportFailuresWithoutSecrets(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		outcome application.TransportOutcome
		class   string
	}{{"timeout", 408, application.TransportTransientFailure, "provider_retryable"}, {"early", 425, application.TransportTransientFailure, "provider_retryable"}, {"limited", 429, application.TransportTransientFailure, "provider_retryable"}, {"server", 503, application.TransportTransientFailure, "provider_retryable"}, {"reject", 422, application.TransportPermanentFailure, "provider_rejected"}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte("secret response"))
			}))
			defer server.Close()
			result := newTransport(t, server.Client(), time.Second, 1).Send(context.Background(), delivery(t, server.URL, "secret", domain.NotificationOpen, time.Now()))
			if result.Outcome != test.outcome || result.ErrorClass != test.class {
				t.Fatalf("Send() = %#v", result)
			}
			if strings.Contains(result.Diagnostic, "secret") || strings.Contains(result.Diagnostic, server.URL) {
				t.Fatalf("diagnostic leaked secret: %q", result.Diagnostic)
			}
		})
	}

	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(started)
		<-time.After(time.Second)
	}))
	defer server.Close()
	result := newTransport(t, server.Client(), 20*time.Millisecond, 1).Send(context.Background(), delivery(t, server.URL, "deadline-secret", domain.NotificationOpen, time.Now()))
	<-started
	if result.Outcome != application.TransportTransientFailure || result.ErrorClass != "deadline_exceeded" || strings.Contains(result.Diagnostic, "deadline-secret") {
		t.Fatalf("deadline result = %#v", result)
	}
}

func TestTransportRejectsInvalidConfigurationAndBoundsParallelCalls(t *testing.T) {
	transport := newTransport(t, http.DefaultClient, time.Second, 2)
	invalid := delivery(t, "https://example.test", "token", domain.NotificationOpen, time.Now())
	invalid.Configuration = []byte(`{"endpoint":"http://user:password@example.test"}`)
	result := transport.Send(context.Background(), invalid)
	if result.Outcome != application.TransportPermanentFailure || result.ErrorClass != "configuration_invalid" || strings.Contains(result.Diagnostic, "password") {
		t.Fatalf("invalid result = %#v", result)
	}

	var active, maximum atomic.Int32
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		value := active.Add(1)
		defer active.Add(-1)
		for {
			old := maximum.Load()
			if value <= old || maximum.CompareAndSwap(old, value) {
				break
			}
		}
		<-release
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	transport = newTransport(t, server.Client(), time.Second, 2)
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			if got := transport.Send(context.Background(), delivery(t, server.URL, "message", domain.NotificationOpen, time.Now())); got.Outcome != application.TransportDelivered {
				t.Errorf("Send() = %#v", got)
			}
		}()
	}
	deadline := time.Now().Add(time.Second)
	for maximum.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(release)
	group.Wait()
	if maximum.Load() != 2 {
		t.Fatalf("maximum parallel calls = %d", maximum.Load())
	}
}

type capturedRequest struct {
	Path          string
	Authorization string
	Body          []byte
}

type alertPayload struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    string            `json:"startsAt"`
	EndsAt      string            `json:"endsAt"`
}

func decodeAlert(t *testing.T, body []byte) alertPayload {
	t.Helper()
	var alerts []alertPayload
	if err := json.Unmarshal(body, &alerts); err != nil || len(alerts) != 1 {
		t.Fatalf("decode alert %s: %v", body, err)
	}
	return alerts[0]
}

func newTransport(t *testing.T, client *http.Client, timeout time.Duration, parallel int) *alertmanageradapter.Transport {
	t.Helper()
	transport, err := alertmanageradapter.NewTransport(alertmanageradapter.TransportConfig{HTTPClient: client, Timeout: timeout, MaxParallel: parallel})
	if err != nil {
		t.Fatal(err)
	}
	return transport
}

func delivery(t *testing.T, endpoint, token string, action domain.NotificationAction, occurredAt time.Time) application.TransportDelivery {
	t.Helper()
	configuration, err := json.Marshal(map[string]string{"endpoint": endpoint, "bearerToken": token})
	if err != nil {
		t.Fatal(err)
	}
	return application.TransportDelivery{
		DeliveryID: "delivery-open", Configuration: configuration,
		Title: "Router health", Message: "router is down",
		Snapshot: domain.RenderSnapshot{
			Action: action, IncidentID: "incident-1", MonitorID: "monitor-1",
			Severity: domain.IncidentCritical, State: domain.HealthDown, OccurredAt: occurredAt,
		},
	}
}
