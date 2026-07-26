package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	monitoringv1alpha1 "github.com/araihu/xisnove/operator/api/v1alpha1"
	"github.com/araihu/xisnove/operator/internal/controller"
	"github.com/araihu/xisnove/operator/internal/controlplane"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

func TestParseConfigUsesNamespacedSecureRuntimeDefaults(t *testing.T) {
	t.Parallel()

	environment := map[string]string{
		"XISNOVE_URL":                          "https://control.example.test",
		"XISNOVE_PROVISIONING_CREDENTIAL_FILE": "/var/run/xisnove-provisioner/token",
		"POD_NAMESPACE":                        "monitoring",
		"XISNOVE_AGENT_IMAGE":                  "ghcr.io/araihu/xisnove-agent:v1",
	}
	config, err := parseConfig(nil, func(key string) string { return environment[key] })
	if err != nil {
		t.Fatal(err)
	}

	if !config.leaderElection {
		t.Fatal("leader election must be enabled by default")
	}
	if config.leaderElectionNamespace != "monitoring" {
		t.Fatalf("leader election namespace = %q", config.leaderElectionNamespace)
	}
	if !reflect.DeepEqual(config.watchNamespaces, []string{"monitoring"}) {
		t.Fatalf("watch namespaces = %#v", config.watchNamespaces)
	}
	if config.pollInterval != 30*time.Second || config.heartbeatStaleAfter != 5*time.Minute {
		t.Fatalf("threshold defaults = poll %s heartbeat %s", config.pollInterval, config.heartbeatStaleAfter)
	}
	if config.gracefulShutdownTimeout != 30*time.Second {
		t.Fatalf("graceful shutdown timeout = %s", config.gracefulShutdownTimeout)
	}
	if config.requestTimeout != 15*time.Second {
		t.Fatalf("control-plane request timeout = %s", config.requestTimeout)
	}
}

func TestParseConfigRejectsMissingCredentialFile(t *testing.T) {
	t.Parallel()

	_, err := parseConfig(nil, func(key string) string {
		if key == "XISNOVE_URL" {
			return "https://control.example.test"
		}
		if key == "POD_NAMESPACE" {
			return "monitoring"
		}
		return ""
	})
	if !errors.Is(err, errCredentialFileRequired) {
		t.Fatalf("parseConfig error = %v", err)
	}
}

func TestParseConfigRejectsUnboundedRuntimeValues(t *testing.T) {
	t.Parallel()

	environment := func(key string) string {
		switch key {
		case "XISNOVE_URL":
			return "https://control.example.test"
		case "XISNOVE_PROVISIONING_CREDENTIAL_FILE":
			return "/var/run/xisnove-provisioner/credential"
		case "POD_NAMESPACE":
			return "monitoring"
		default:
			return ""
		}
	}
	for _, arguments := range [][]string{
		{"--request-timeout=0s"},
		{"--request-timeout=-1s"},
		{"--graceful-shutdown-timeout=0s"},
		{"--graceful-shutdown-timeout=-1s"},
	} {
		if _, err := parseConfig(arguments, environment); !errors.Is(err, errPositiveDurationRequired) {
			t.Fatalf("parseConfig(%v) error = %v", arguments, err)
		}
	}
}

func TestParseConfigRejectsEmptyWatchScopeWhenLeaderElectionIsDisabled(t *testing.T) {
	t.Parallel()

	_, err := parseConfig([]string{"--leader-elect=false"}, func(key string) string {
		switch key {
		case "XISNOVE_URL":
			return "https://control.example.test"
		case "XISNOVE_PROVISIONING_CREDENTIAL_FILE":
			return "/var/run/xisnove-provisioner/credential"
		default:
			return ""
		}
	})
	if !errors.Is(err, errWatchNamespaceRequired) {
		t.Fatalf("parseConfig error = %v", err)
	}
}

func TestParseConfigAcceptsRequestTimeoutFromEnvironment(t *testing.T) {
	t.Parallel()

	config, err := parseConfig(nil, func(key string) string {
		switch key {
		case "XISNOVE_URL":
			return "https://control.example.test"
		case "XISNOVE_PROVISIONING_CREDENTIAL_FILE":
			return "/var/run/xisnove-provisioner/credential"
		case "POD_NAMESPACE":
			return "monitoring"
		case "XISNOVE_REQUEST_TIMEOUT":
			return "7s"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.requestTimeout != 7*time.Second {
		t.Fatalf("request timeout = %s", config.requestTimeout)
	}
}

func TestManagerOptionsUseNamespacedCacheLeaseProbesAndGracefulShutdown(t *testing.T) {
	t.Parallel()

	config := runtimeConfig{
		watchNamespaces:         []string{"edge-a", "edge-b"},
		leaderElection:          true,
		leaderElectionNamespace: "monitoring",
		metricsBindAddress:      ":8080",
		healthProbeBindAddress:  ":8081",
		gracefulShutdownTimeout: 45 * time.Second,
	}
	options := buildManagerOptions(config, mustScheme(t))

	if !options.LeaderElection || options.LeaderElectionResourceLock != "leases" || options.LeaderElectionNamespace != "monitoring" {
		t.Fatalf("leader election options = enabled %v lock %q namespace %q", options.LeaderElection, options.LeaderElectionResourceLock, options.LeaderElectionNamespace)
	}
	if options.HealthProbeBindAddress != ":8081" || options.ReadinessEndpointName != "readyz" || options.LivenessEndpointName != "healthz" {
		t.Fatalf("health options = %#v", options)
	}
	if options.GracefulShutdownTimeout == nil || *options.GracefulShutdownTimeout != 45*time.Second {
		t.Fatalf("graceful shutdown = %v", options.GracefulShutdownTimeout)
	}
	for _, namespace := range config.watchNamespaces {
		if _, ok := options.Cache.DefaultNamespaces[namespace]; !ok {
			t.Fatalf("cache does not watch namespace %q", namespace)
		}
	}
	if _, all := options.Cache.DefaultNamespaces[cache.AllNamespaces]; all {
		t.Fatal("cache unexpectedly watches all namespaces")
	}
}

func TestSchemeRegistersOperatorAndWorkloadTypes(t *testing.T) {
	t.Parallel()

	scheme := mustScheme(t)
	for _, gvk := range []schema.GroupVersionKind{
		monitoringv1alpha1.GroupVersion.WithKind("Monitor"),
		monitoringv1alpha1.GroupVersion.WithKind("Agent"),
		appsv1.SchemeGroupVersion.WithKind("Deployment"),
		corev1.SchemeGroupVersion.WithKind("Secret"),
	} {
		if !scheme.Recognizes(gvk) {
			t.Fatalf("scheme does not recognize %s", gvk)
		}
	}
}

func TestWireControllersRegistersMonitorAndAgentWithAPIReader(t *testing.T) {
	t.Parallel()

	scheme := mustScheme(t)
	kube := fake.NewClientBuilder().WithScheme(scheme).Build()
	apiReader := fake.NewClientBuilder().WithScheme(scheme).Build()
	var remote controlplane.Client
	config := runtimeConfig{controlPlaneURL: "https://control.example.test", agentImage: "agent:v1", pollInterval: 17 * time.Second, heartbeatStaleAfter: 2 * time.Minute}
	var monitor *controller.MonitorReconciler
	var agent *controller.AgentReconciler
	err := wireControllers(kube, apiReader, scheme, remote, config, controllerSetups{
		monitor: func(candidate *controller.MonitorReconciler) error { monitor = candidate; return nil },
		agent:   func(candidate *controller.AgentReconciler) error { agent = candidate; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if monitor == nil || agent == nil {
		t.Fatalf("registered reconcilers = monitor %v agent %v", monitor != nil, agent != nil)
	}
	if agent.APIReader != apiReader {
		t.Fatal("Agent reconciler does not use the uncached APIReader")
	}
	if monitor.PollInterval != 17*time.Second || agent.PollInterval != 17*time.Second || agent.HeartbeatStaleAfter != 2*time.Minute {
		t.Fatalf("threshold wiring = monitor %s agent %s heartbeat %s", monitor.PollInterval, agent.PollInterval, agent.HeartbeatStaleAfter)
	}
}

func TestCredentialFileDoerReloadsAndReplacesAuthorization(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte("first-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	next := &recordingDoer{}
	doer := &credentialFileDoer{path: path, next: next}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://control.example.test/v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer stale-token")
	if _, err := doer.Do(request); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("second-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := doer.Do(request); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(next.authorizations, []string{"Bearer first-token", "Bearer second-token"}) {
		t.Fatalf("authorization sequence = %#v", next.authorizations)
	}
}

func TestControlPlaneHTTPClientUsesConfiguredPositiveTimeout(t *testing.T) {
	t.Parallel()

	client := newControlPlaneHTTPClient(9 * time.Second)
	if client == http.DefaultClient {
		t.Fatal("control-plane requests use http.DefaultClient")
	}
	if client.Timeout != 9*time.Second {
		t.Fatalf("HTTP timeout = %s", client.Timeout)
	}
}

func TestRegisterHealthChecksAddsLivenessAndReadiness(t *testing.T) {
	t.Parallel()

	registrar := &recordingHealthRegistrar{}
	syncer := &stubCacheSyncer{synced: true}
	if err := registerHealthChecks(registrar, syncer); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(registrar.health, []string{"ping"}) || !reflect.DeepEqual(registrar.ready, []string{"cache"}) {
		t.Fatalf("registered checks = health %v ready %v", registrar.health, registrar.ready)
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://operator/readyz", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := registrar.readyChecks[0](request); err != nil {
		t.Fatalf("ready checker rejected synchronized cache: %v", err)
	}
}

func TestCacheReadinessFailsWhenCacheHasNotSynchronized(t *testing.T) {
	t.Parallel()

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://operator/readyz", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := cacheReadyz(&stubCacheSyncer{synced: false})(request); err == nil {
		t.Fatal("readiness unexpectedly passed before cache synchronization")
	}
}

func TestCacheReadinessUsesRequestCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://operator/readyz", nil)
	if err != nil {
		t.Fatal(err)
	}
	syncer := &contextAwareCacheSyncer{}
	if err := cacheReadyz(syncer)(request); err == nil {
		t.Fatal("readiness unexpectedly passed for a canceled request")
	}
	if !syncer.sawCancellation {
		t.Fatal("readiness did not pass the request context to cache synchronization")
	}
}

type recordingHealthRegistrar struct {
	health       []string
	ready        []string
	healthChecks []healthz.Checker
	readyChecks  []healthz.Checker
}

func (r *recordingHealthRegistrar) AddHealthzCheck(name string, checker healthz.Checker) error {
	r.health = append(r.health, name)
	r.healthChecks = append(r.healthChecks, checker)
	return nil
}

func (r *recordingHealthRegistrar) AddReadyzCheck(name string, checker healthz.Checker) error {
	r.ready = append(r.ready, name)
	r.readyChecks = append(r.readyChecks, checker)
	return nil
}

type stubCacheSyncer struct{ synced bool }

func (s *stubCacheSyncer) WaitForCacheSync(context.Context) bool { return s.synced }

type contextAwareCacheSyncer struct{ sawCancellation bool }

func (s *contextAwareCacheSyncer) WaitForCacheSync(ctx context.Context) bool {
	s.sawCancellation = ctx.Err() != nil
	return false
}

type recordingDoer struct {
	authorizations []string
}

func (d *recordingDoer) Do(request *http.Request) (*http.Response, error) {
	d.authorizations = append(d.authorizations, request.Header.Get("Authorization"))
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
}

func mustScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme, err := newScheme()
	if err != nil {
		t.Fatal(err)
	}
	return scheme
}
