package shoutrrr_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	shoutrrradapter "github.com/araihu/xisnove/internal/adapters/shoutrrr"
)

func TestTransportPreservesPayloadAndClassifiesStatuses(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		outcome application.TransportOutcome
		class   string
	}{{"success", 204, application.TransportDelivered, ""}, {"retry", 503, application.TransportTransientFailure, "provider_retryable"}, {"reject", 400, application.TransportPermanentFailure, "provider_rejected"}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var body []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				w.WriteHeader(test.status)
			}))
			defer server.Close()
			transport := newTransport(t, server.Client(), time.Second, 2)
			result := transport.Send(context.Background(), delivery(t, genericURL(t, server.URL, "token=secret"), "router down", "critical title"))
			if result.Outcome != test.outcome || result.ErrorClass != test.class {
				t.Fatalf("Send() = %#v", result)
			}
			if strings.Contains(result.Diagnostic, "secret") || strings.Contains(result.Diagnostic, "router down") || strings.Contains(result.Diagnostic, server.URL) {
				t.Fatalf("diagnostic leaked sensitive input: %q", result.Diagnostic)
			}
			if test.status < 400 {
				var payload map[string]string
				if err := json.Unmarshal(body, &payload); err != nil || payload["message"] != "router down" || payload["title"] != "critical title" {
					t.Fatalf("payload = %s, %v", body, err)
				}
			}
		})
	}
}

func TestTransportHonorsDeadlineAndRejectsUnreviewedScheme(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	transport := newTransport(t, server.Client(), 40*time.Millisecond, 1)
	result := transport.Send(context.Background(), delivery(t, genericURL(t, server.URL, ""), "secret body", ""))
	<-started
	close(release)
	if result.Outcome != application.TransportTransientFailure || result.ErrorClass != "deadline_exceeded" || strings.Contains(result.Diagnostic, "secret body") {
		t.Fatalf("deadline result = %#v", result)
	}
	result = transport.Send(context.Background(), delivery(t, "smtp://user:password@example.com", "body", ""))
	if result.Outcome != application.TransportPermanentFailure || result.ErrorClass != "scheme_not_reviewed" || strings.Contains(result.Diagnostic, "password") {
		t.Fatalf("unreviewed result = %#v", result)
	}
}

func TestTransportBoundsParallelProviderCalls(t *testing.T) {
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
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	transport := newTransport(t, server.Client(), time.Second, 2)
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			result := transport.Send(context.Background(), delivery(t, genericURL(t, server.URL, ""), "body", ""))
			if result.Outcome != application.TransportDelivered {
				t.Errorf("Send() = %#v", result)
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

func newTransport(t *testing.T, client *http.Client, timeout time.Duration, parallel int) *shoutrrradapter.Transport {
	t.Helper()
	transport, err := shoutrrradapter.NewTransport(shoutrrradapter.TransportConfig{HTTPClient: client, Timeout: timeout, MaxParallel: parallel})
	if err != nil {
		t.Fatal(err)
	}
	return transport
}

func delivery(t *testing.T, serviceURL, message, title string) application.TransportDelivery {
	t.Helper()
	configuration, err := json.Marshal(map[string]string{"serviceUrl": serviceURL})
	if err != nil {
		t.Fatal(err)
	}
	return application.TransportDelivery{Configuration: configuration, Message: message, Title: title}
}

func genericURL(t *testing.T, target, querySecret string) string {
	t.Helper()
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	query := "disabletls=yes&template=json"
	if querySecret != "" {
		query += "&" + querySecret
	}
	return fmt.Sprintf("generic://%s/?%s", parsed.Host, query)
}
