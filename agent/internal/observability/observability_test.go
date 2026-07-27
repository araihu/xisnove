package observability

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReadinessRequiresAllRuntimePhasesAndFailsBeforeDrain(t *testing.T) {
	state := NewState()
	handler := Handler(state)
	assertStatus(t, handler, "/livez", http.StatusNoContent)
	assertStatus(t, handler, "/readyz", http.StatusServiceUnavailable)

	state.MarkCredentialLoaded()
	assertStatus(t, handler, "/readyz", http.StatusServiceUnavailable)
	state.MarkClientInitialized()
	assertStatus(t, handler, "/readyz", http.StatusServiceUnavailable)
	state.SetAcceptingLeases(true)
	assertStatus(t, handler, "/readyz", http.StatusNoContent)

	state.BeginDrain()
	assertStatus(t, handler, "/readyz", http.StatusServiceUnavailable)
	assertStatus(t, handler, "/livez", http.StatusNoContent)
}

func TestMetricsAreBoundedPrometheusTextWithoutRuntimeSecrets(t *testing.T) {
	state := NewState()
	state.MarkCredentialLoaded()
	state.MarkClientInitialized()
	state.SetAcceptingLeases(true)
	recorder := httptest.NewRecorder()
	Handler(state).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("metrics status = %d", recorder.Code)
	}
	body := recorder.Body.String()
	for _, metric := range []string{"xisnove_agent_ready 1", "xisnove_agent_accepting_leases 1"} {
		if !strings.Contains(body, metric) {
			t.Fatalf("metrics missing %q:\n%s", metric, body)
		}
	}
	if len(body) > 2048 {
		t.Fatalf("metrics response length = %d, want <= 2048", len(body))
	}
	if strings.Contains(strings.ToLower(body), "credential") {
		t.Fatalf("metrics expose credential state: %q", body)
	}
}

func TestServerBindsRequestedAddressAndStopsWithinBudget(t *testing.T) {
	state := NewState()
	server, err := Listen("127.0.0.1:0", state)
	if err != nil {
		t.Fatal(err)
	}
	if server.server.ReadHeaderTimeout != 5*time.Second || server.server.ReadTimeout != 5*time.Second || server.server.WriteTimeout != 5*time.Second || server.server.IdleTimeout != 30*time.Second {
		t.Fatalf("HTTP bounds = read-header %s read %s write %s idle %s", server.server.ReadHeaderTimeout, server.server.ReadTimeout, server.server.WriteTimeout, server.server.IdleTimeout)
	}
	if server.server.MaxHeaderBytes != 8<<10 {
		t.Fatalf("max header bytes = %d", server.server.MaxHeaderBytes)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()

	response, err := http.Get("http://" + server.Address() + "/livez")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("live status = %d", response.StatusCode)
	}

	started := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("observability server did not stop within one second")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("shutdown elapsed = %s", elapsed)
	}
}

func assertStatus(t *testing.T, handler http.Handler, path string, want int) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	if recorder.Code != want {
		t.Fatalf("GET %s status = %d, want %d", path, recorder.Code, want)
	}
}
