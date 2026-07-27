package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/xisnove/agent/internal/buildinfo"
	"github.com/araihu/xisnove/agent/internal/controlplane"
	"github.com/araihu/xisnove/agent/internal/observability"
)

func TestExecuteVersionSkipsConfigurationAndRuntime(t *testing.T) {
	setAgentBuildInfo(t, "1.2.3", "0123456789abcdef0123456789abcdef01234567", "2026-07-27T03:04:05Z", "false")
	var stdout, stderr bytes.Buffer
	called := false
	exit := execute([]string{"--version"}, &stdout, &stderr, func(string) string {
		t.Fatal("environment read")
		return ""
	}, func(config) error {
		called = true
		return nil
	})
	if exit != 0 || called || stderr.Len() != 0 {
		t.Fatalf("execute = exit %d called %t stderr %q", exit, called, stderr.String())
	}
	want := "xisnove-agent version=1.2.3 commit=0123456789abcdef0123456789abcdef01234567 build_date=2026-07-27T03:04:05Z dirty=false\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestExecuteInvalidVersionAndMalformedFlagsUseSingleDiagnostic(t *testing.T) {
	tests := [][]string{{"--version"}, {"--version", "extra"}, {"--unknown"}}
	for _, arguments := range tests {
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			setAgentBuildInfo(t, "dev", "bad", "bad", "true")
			var stdout, stderr bytes.Buffer
			exit := execute(arguments, &stdout, &stderr, func(string) string { return "" }, func(config) error {
				t.Fatal("runtime initialized")
				return nil
			})
			if exit != 2 || stdout.Len() != 0 || strings.Count(stderr.String(), "\n") != 1 {
				t.Fatalf("execute = exit %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
			}
		})
	}
}

func TestExecuteConfigurationAndRuntimeFailuresUseStableExitClasses(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := execute(nil, &stdout, &stderr, func(string) string { return "" }, func(config) error {
		t.Fatal("runtime initialized")
		return nil
	})
	if exit != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "XISNOVE_URL") || strings.Count(stderr.String(), "\n") != 1 {
		t.Fatalf("configuration failure = exit %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	values := map[string]string{
		"XISNOVE_URL":                   "https://monitor.example.com",
		"XISNOVE_AGENT_CREDENTIAL_FILE": "/run/secrets/agent",
	}
	exit = execute(nil, &stdout, &stderr, func(key string) string { return values[key] }, func(config) error {
		return errors.New("runtime failed")
	})
	if exit != 1 || stdout.Len() != 0 || stderr.String() != "agent stopped: runtime failed\n" {
		t.Fatalf("runtime failure = exit %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
}

func setAgentBuildInfo(t *testing.T, version, commit, date, dirty string) {
	t.Helper()
	oldVersion, oldCommit, oldDate, oldDirty := buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate, buildinfo.Dirty
	buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate, buildinfo.Dirty = version, commit, date, dirty
	t.Cleanup(func() {
		buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate, buildinfo.Dirty = oldVersion, oldCommit, oldDate, oldDirty
	})
}

func TestCredentialProviderReadsOnlyConfiguredBundleFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.json")
	if err := os.WriteFile(path, []byte(`{"credential":"credential-from-file","generation":17}`), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, err := credentialProvider(config{credentialFile: path}).Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Credential != "credential-from-file" || bundle.Generation != 17 {
		t.Fatalf("bundle = %#v", bundle)
	}
}

func TestRunWithContextMarksReadyThenDrainsBeforeExit(t *testing.T) {
	setAgentBuildInfo(t, "1.2.3", "0123456789abcdef0123456789abcdef01234567", "2026-07-27T03:04:05Z", "false")
	credentialPath := filepath.Join(t.TempDir(), "credential.json")
	if err := os.WriteFile(credentialPath, []byte(`{"credential":"agent-credential","generation":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	heartbeats := make(chan controlplane.AgentHeartbeat, 1)
	controlPlane := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/agent/heartbeat":
			var heartbeat controlplane.AgentHeartbeat
			if err := json.NewDecoder(request.Body).Decode(&heartbeat); err != nil {
				http.Error(writer, "invalid heartbeat", http.StatusBadRequest)
				return
			}
			select {
			case heartbeats <- heartbeat:
			default:
			}
			writer.WriteHeader(http.StatusNoContent)
		case "/v1/agent/work:lease":
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(controlPlane.Close)

	state := observability.NewState()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runWithContext(ctx, config{
			controlPlaneURL:      controlPlane.URL,
			credentialFile:       credentialPath,
			observabilityAddress: "127.0.0.1:0",
			capabilities:         []controlplane.AgentCapability{controlplane.AgentCapabilityHttp},
		}, state)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for !state.Ready() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !state.Ready() {
		t.Fatal("Agent never became ready")
	}
	select {
	case heartbeat := <-heartbeats:
		if heartbeat.Version != "1.2.3" {
			t.Fatalf("heartbeat version = %q", heartbeat.Version)
		}
	case <-time.After(time.Second):
		t.Fatal("Agent did not send heartbeat")
	}
	replacement := filepath.Join(t.TempDir(), "invalid-credential.json")
	if err := os.WriteFile(replacement, []byte(`{"credential":"","generation":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, credentialPath); err != nil {
		t.Fatal(err)
	}
	unreadyDeadline := time.Now().Add(2 * time.Second)
	for state.Ready() && time.Now().Before(unreadyDeadline) {
		time.Sleep(time.Millisecond)
	}
	if state.Ready() {
		t.Fatal("Agent remained ready after credential reload failed")
	}
	cancel()
	drainDeadline := time.Now().Add(100 * time.Millisecond)
	for state.Ready() && time.Now().Before(drainDeadline) {
		time.Sleep(time.Millisecond)
	}
	if state.Ready() {
		t.Fatal("Agent remained ready after drain began")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Agent did not drain within one second")
	}
}

func TestLoadConfig(t *testing.T) {
	values := map[string]string{
		"XISNOVE_URL":                         "https://monitor.example.com/",
		"XISNOVE_AGENT_CREDENTIAL_FILE":       "/run/secrets/agent",
		"XISNOVE_AGENT_ALLOWED_PRIVATE_CIDRS": "10.0.0.0/8, 192.168.0.0/16",
	}

	config, err := loadConfig(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if config.controlPlaneURL != "https://monitor.example.com" {
		t.Fatalf("control plane URL = %q", config.controlPlaneURL)
	}
	if config.credentialFile != "/run/secrets/agent" {
		t.Fatalf("credential file = %q", config.credentialFile)
	}
	if config.observabilityAddress != "127.0.0.1:9090" {
		t.Fatalf("observability address = %q", config.observabilityAddress)
	}
	wantCapabilities := []controlplane.AgentCapability{
		controlplane.AgentCapabilityHttp,
		controlplane.AgentCapabilityTcp,
		controlplane.AgentCapabilityDns,
	}
	if len(config.capabilities) != len(wantCapabilities) {
		t.Fatalf("capabilities = %#v", config.capabilities)
	}
	for index := range wantCapabilities {
		if config.capabilities[index] != wantCapabilities[index] {
			t.Fatalf("capabilities[%d] = %s", index, config.capabilities[index])
		}
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("192.168.0.0/16"),
	}
	if len(config.allowedPrivate) != len(want) {
		t.Fatalf("allowed private = %#v", config.allowedPrivate)
	}
	for index := range want {
		if config.allowedPrivate[index] != want[index] {
			t.Fatalf("allowed private[%d] = %s", index, config.allowedPrivate[index])
		}
	}
}

func TestLoadConfigAllowsExplicitContainerObservabilityAddress(t *testing.T) {
	values := map[string]string{
		"XISNOVE_URL":                         "https://monitor.example.com",
		"XISNOVE_AGENT_CREDENTIAL_FILE":       "/run/secrets/agent",
		"XISNOVE_AGENT_OBSERVABILITY_ADDRESS": "0.0.0.0:9090",
	}
	config, err := loadConfig(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if config.observabilityAddress != "0.0.0.0:9090" {
		t.Fatalf("observability address = %q", config.observabilityAddress)
	}
}

func TestLoadConfigRejectsMissingOrUnsafeValues(t *testing.T) {
	tests := map[string]map[string]string{
		"missing URL": {
			"XISNOVE_AGENT_CREDENTIAL_FILE": "/run/secrets/agent",
		},
		"unsupported URL scheme": {
			"XISNOVE_URL":                   "file:///tmp/control-plane",
			"XISNOVE_AGENT_CREDENTIAL_FILE": "/run/secrets/agent",
		},
		"missing credential file": {
			"XISNOVE_URL": "https://monitor.example.com",
		},
		"invalid CIDR": {
			"XISNOVE_URL":                         "https://monitor.example.com",
			"XISNOVE_AGENT_CREDENTIAL_FILE":       "/run/secrets/agent",
			"XISNOVE_AGENT_ALLOWED_PRIVATE_CIDRS": "not-a-cidr",
		},
		"invalid capability": {
			"XISNOVE_URL":                   "https://monitor.example.com",
			"XISNOVE_AGENT_CREDENTIAL_FILE": "/run/secrets/agent",
			"XISNOVE_AGENT_CAPABILITIES":    "http,exec",
		},
		"invalid observability address": {
			"XISNOVE_URL":                         "https://monitor.example.com",
			"XISNOVE_AGENT_CREDENTIAL_FILE":       "/run/secrets/agent",
			"XISNOVE_AGENT_OBSERVABILITY_ADDRESS": "example.com:9090",
		},
	}

	for name, values := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := loadConfig(func(key string) string { return values[key] })
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestLoadConfigEnablesKubernetesDiscoveryOnlyWhenConfigured(t *testing.T) {
	base := map[string]string{
		"XISNOVE_URL": "https://monitor.example.com", "XISNOVE_AGENT_CREDENTIAL_FILE": "/run/secrets/agent",
	}
	config, err := loadConfig(func(key string) string { return base[key] })
	if err != nil {
		t.Fatal(err)
	}
	if config.kubernetesDiscovery.enabled {
		t.Fatalf("raw agent unexpectedly enabled Kubernetes discovery: %#v", config)
	}
	base["XISNOVE_AGENT_CAPABILITIES"] = "http,kubernetes-discovery"
	base["XISNOVE_DISCOVERY_NAMESPACES"] = "payments,default"
	base["XISNOVE_DISCOVERY_RESOURCES"] = "services,ingresses"
	config, err = loadConfig(func(key string) string { return base[key] })
	if err != nil {
		t.Fatal(err)
	}
	if !config.kubernetesDiscovery.enabled || len(config.kubernetesDiscovery.namespaces) != 2 || len(config.kubernetesDiscovery.resources) != 2 {
		t.Fatalf("Kubernetes discovery config = %#v", config.kubernetesDiscovery)
	}
}
