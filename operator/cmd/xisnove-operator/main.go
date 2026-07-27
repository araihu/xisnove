package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	monitoringv1alpha1 "github.com/araihu/xisnove/operator/api/v1alpha1"
	"github.com/araihu/xisnove/operator/internal/buildinfo"
	"github.com/araihu/xisnove/operator/internal/controller"
	"github.com/araihu/xisnove/operator/internal/controlplane"
	controlplanesdk "github.com/araihu/xisnove/operator/internal/controlplane/sdk"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	errControlPlaneURLRequired  = errors.New("control plane URL is required")
	errCredentialFileRequired   = errors.New("provisioning credential file is required")
	errNamespaceRequired        = errors.New("operator namespace is required when leader election is enabled")
	errWatchNamespaceRequired   = errors.New("at least one watch namespace is required")
	errPositiveDurationRequired = errors.New("request, poll, heartbeat, and graceful-shutdown durations must be positive")
)

type runtimeConfig struct {
	controlPlaneURL         string
	credentialFile          string
	requestTimeout          time.Duration
	watchNamespaces         []string
	agentImage              string
	pollInterval            time.Duration
	heartbeatStaleAfter     time.Duration
	metricsBindAddress      string
	healthProbeBindAddress  string
	leaderElection          bool
	leaderElectionNamespace string
	leaderElectionID        string
	gracefulShutdownTimeout time.Duration
}

type controllerSetups struct {
	monitor func(*controller.MonitorReconciler) error
	agent   func(*controller.AgentReconciler) error
}

type healthRegistrar interface {
	AddHealthzCheck(string, healthz.Checker) error
	AddReadyzCheck(string, healthz.Checker) error
}

type cacheSyncer interface {
	WaitForCacheSync(context.Context) bool
}

type credentialFileDoer struct {
	path string
	next controlplanesdk.HTTPDoer
}

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr, os.Getenv, func(config runtimeConfig) error {
		return run(ctrl.SetupSignalHandler(), config)
	}))
}

func execute(arguments []string, stdout, stderr io.Writer, getenv func(string) string, start func(runtimeConfig) error) int {
	if len(arguments) > 0 && arguments[0] == "--version" {
		if len(arguments) != 1 {
			fmt.Fprintln(stderr, "error: --version accepts no arguments")
			return 2
		}
		value, err := buildinfo.String("xisnove-operator")
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, value)
		return 0
	}
	config, err := parseConfig(arguments, getenv)
	if err != nil {
		fmt.Fprintf(stderr, "invalid operator configuration: %v\n", err)
		return 2
	}
	if err := start(config); err != nil {
		fmt.Fprintf(stderr, "operator stopped: %v\n", err)
		return 1
	}
	return 0
}

func run(ctx context.Context, config runtimeConfig) error {
	scheme, err := newScheme()
	if err != nil {
		return err
	}
	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("load Kubernetes configuration: %w", err)
	}
	manager, err := ctrl.NewManager(restConfig, buildManagerOptions(config, scheme))
	if err != nil {
		return fmt.Errorf("create controller manager: %w", err)
	}

	credential, err := readCredential(config.credentialFile)
	if err != nil {
		return err
	}
	doer := &credentialFileDoer{path: config.credentialFile, next: newControlPlaneHTTPClient(config.requestTimeout)}
	remote, err := controlplanesdk.New(config.controlPlaneURL, credential, controlplanesdk.WithHTTPClient(doer))
	if err != nil {
		return fmt.Errorf("create control-plane SDK adapter: %w", err)
	}
	if err := wireControllers(manager.GetClient(), manager.GetAPIReader(), manager.GetScheme(), remote, config, controllerSetups{
		monitor: func(reconciler *controller.MonitorReconciler) error { return reconciler.SetupWithManager(manager) },
		agent:   func(reconciler *controller.AgentReconciler) error { return reconciler.SetupWithManager(manager) },
	}); err != nil {
		return err
	}
	if err := registerHealthChecks(manager, manager.GetCache()); err != nil {
		return err
	}
	if err := manager.Start(ctx); err != nil {
		return fmt.Errorf("run controller manager: %w", err)
	}
	return nil
}

func registerHealthChecks(registrar healthRegistrar, cache cacheSyncer) error {
	if err := registrar.AddHealthzCheck("ping", healthz.Ping); err != nil {
		return fmt.Errorf("register health check: %w", err)
	}
	if err := registrar.AddReadyzCheck("cache", cacheReadyz(cache)); err != nil {
		return fmt.Errorf("register readiness check: %w", err)
	}
	return nil
}

func cacheReadyz(cache cacheSyncer) healthz.Checker {
	return func(request *http.Request) error {
		if !cache.WaitForCacheSync(request.Context()) {
			return errors.New("manager cache is not synchronized")
		}
		return nil
	}
}

func parseConfig(arguments []string, getenv func(string) string) (runtimeConfig, error) {
	config := runtimeConfig{}
	watchNamespaces := getenv("XISNOVE_WATCH_NAMESPACES")
	operatorNamespace := strings.TrimSpace(getenv("POD_NAMESPACE"))
	if strings.TrimSpace(watchNamespaces) == "" {
		watchNamespaces = operatorNamespace
	}
	requestTimeout := 15 * time.Second
	if raw := strings.TrimSpace(getenv("XISNOVE_REQUEST_TIMEOUT")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return runtimeConfig{}, fmt.Errorf("parse XISNOVE_REQUEST_TIMEOUT: %w", err)
		}
		requestTimeout = parsed
	}

	flags := flag.NewFlagSet("xisnove-operator", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&config.controlPlaneURL, "control-plane-url", getenv("XISNOVE_URL"), "Xisnove control-plane URL")
	flags.StringVar(&config.credentialFile, "provisioning-credential-file", getenv("XISNOVE_PROVISIONING_CREDENTIAL_FILE"), "mounted provisioning credential file")
	flags.StringVar(&watchNamespaces, "watch-namespaces", watchNamespaces, "comma-separated namespaces watched by the operator")
	flags.StringVar(&config.agentImage, "agent-image", getenv("XISNOVE_AGENT_IMAGE"), "default Agent image")
	flags.DurationVar(&config.requestTimeout, "request-timeout", requestTimeout, "control-plane HTTP request timeout")
	flags.DurationVar(&config.pollInterval, "poll-interval", 30*time.Second, "remote observation polling interval")
	flags.DurationVar(&config.heartbeatStaleAfter, "heartbeat-stale-after", 5*time.Minute, "Agent heartbeat staleness threshold")
	flags.StringVar(&config.metricsBindAddress, "metrics-bind-address", ":8080", "metrics server bind address")
	flags.StringVar(&config.healthProbeBindAddress, "health-probe-bind-address", ":8081", "health probe server bind address")
	flags.BoolVar(&config.leaderElection, "leader-elect", true, "enable namespaced Lease leader election")
	flags.StringVar(&config.leaderElectionNamespace, "leader-election-namespace", operatorNamespace, "namespace containing the leader-election Lease")
	flags.StringVar(&config.leaderElectionID, "leader-election-id", "xisnove-operator.monitoring.xisnove.io", "leader-election Lease name")
	flags.DurationVar(&config.gracefulShutdownTimeout, "graceful-shutdown-timeout", 30*time.Second, "time allowed for graceful shutdown")
	if err := flags.Parse(arguments); err != nil {
		return runtimeConfig{}, fmt.Errorf("parse flags: %w", err)
	}

	config.controlPlaneURL = strings.TrimSpace(config.controlPlaneURL)
	config.credentialFile = strings.TrimSpace(config.credentialFile)
	config.watchNamespaces = splitNames(watchNamespaces)
	if config.controlPlaneURL == "" {
		return runtimeConfig{}, errControlPlaneURLRequired
	}
	if config.credentialFile == "" {
		return runtimeConfig{}, errCredentialFileRequired
	}
	if len(config.watchNamespaces) == 0 {
		return runtimeConfig{}, errWatchNamespaceRequired
	}
	if config.leaderElection && strings.TrimSpace(config.leaderElectionNamespace) == "" {
		return runtimeConfig{}, errNamespaceRequired
	}
	if config.requestTimeout <= 0 || config.pollInterval <= 0 || config.heartbeatStaleAfter <= 0 || config.gracefulShutdownTimeout <= 0 {
		return runtimeConfig{}, errPositiveDurationRequired
	}
	return config, nil
}

func newControlPlaneHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

func buildManagerOptions(config runtimeConfig, scheme *runtime.Scheme) ctrl.Options {
	namespaces := make(map[string]cache.Config, len(config.watchNamespaces))
	for _, namespace := range config.watchNamespaces {
		namespaces[namespace] = cache.Config{}
	}
	shutdown := config.gracefulShutdownTimeout
	return ctrl.Options{
		Scheme:                        scheme,
		Cache:                         cache.Options{DefaultNamespaces: namespaces, ReaderFailOnMissingInformer: true},
		Metrics:                       metricsserver.Options{BindAddress: config.metricsBindAddress},
		HealthProbeBindAddress:        config.healthProbeBindAddress,
		ReadinessEndpointName:         "/readyz",
		LivenessEndpointName:          "/healthz",
		LeaderElection:                config.leaderElection,
		LeaderElectionResourceLock:    "leases",
		LeaderElectionNamespace:       config.leaderElectionNamespace,
		LeaderElectionID:              config.leaderElectionID,
		LeaderElectionReleaseOnCancel: true,
		GracefulShutdownTimeout:       &shutdown,
	}
}

func newScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register Kubernetes scheme: %w", err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register workload scheme: %w", err)
	}
	if err := monitoringv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register Xisnove scheme: %w", err)
	}
	return scheme, nil
}

func wireControllers(kube client.Client, apiReader client.Reader, scheme *runtime.Scheme, remote controlplane.Client, config runtimeConfig, setups controllerSetups) error {
	monitor := &controller.MonitorReconciler{Client: kube, Scheme: scheme, ControlPlane: remote, PollInterval: config.pollInterval}
	if err := setups.monitor(monitor); err != nil {
		return fmt.Errorf("register Monitor controller: %w", err)
	}
	agent := &controller.AgentReconciler{
		Client:              kube,
		APIReader:           apiReader,
		Scheme:              scheme,
		ControlPlane:        remote,
		ControlPlaneURL:     config.controlPlaneURL,
		DefaultAgentImage:   config.agentImage,
		PollInterval:        config.pollInterval,
		HeartbeatStaleAfter: config.heartbeatStaleAfter,
	}
	if err := setups.agent(agent); err != nil {
		return fmt.Errorf("register Agent controller: %w", err)
	}
	return nil
}

func (d *credentialFileDoer) Do(request *http.Request) (*http.Response, error) {
	credential, err := readCredential(d.path)
	if err != nil {
		return nil, err
	}
	copy := request.Clone(request.Context())
	copy.Header = request.Header.Clone()
	copy.Header.Set("Authorization", "Bearer "+credential)
	return d.next.Do(copy)
}

func readCredential(path string) (string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read provisioning credential: %w", err)
	}
	credential := strings.TrimSpace(string(contents))
	if credential == "" {
		return "", errors.New("provisioning credential file is empty")
	}
	return credential, nil
}

func splitNames(value string) []string {
	seen := map[string]struct{}{}
	var result []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}
