package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/araihu/xisnove/agent/credentials"
	"github.com/araihu/xisnove/agent/discovery"
	kubernetesdiscovery "github.com/araihu/xisnove/agent/discovery/kubernetes"
	"github.com/araihu/xisnove/agent/internal/controlplane"
	"github.com/araihu/xisnove/agent/probe"
	"github.com/araihu/xisnove/agent/worker"
	"k8s.io/client-go/dynamic"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var version = "dev"

type config struct {
	controlPlaneURL     string
	credentialFile      string
	allowedPrivate      []netip.Prefix
	capabilities        []controlplane.AgentCapability
	kubernetesDiscovery kubernetesDiscoveryConfig
}

type kubernetesDiscoveryConfig struct {
	enabled    bool
	watch      bool
	namespaces []string
	resources  []kubernetesdiscovery.Resource
}

func main() {
	if err := run(); err != nil {
		slog.Error("agent stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	config, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	client, err := controlplane.NewClientWithResponses(config.controlPlaneURL)
	if err != nil {
		return fmt.Errorf("create control-plane client: %w", err)
	}

	policy := probe.DefaultPolicy()
	policy.AllowedPrivate = config.allowedPrivate
	var httpExecutor probe.Executor
	var tcpExecutor probe.Executor
	var dnsExecutor probe.Executor
	for _, capability := range config.capabilities {
		switch capability {
		case controlplane.AgentCapabilityHttp:
			httpExecutor = probe.NewHTTPExecutor(policy)
		case controlplane.AgentCapabilityTcp:
			tcpExecutor = probe.NewTCPExecutor(policy)
		case controlplane.AgentCapabilityDns:
			dnsExecutor = probe.NewDNSExecutor(policy)
		}
	}
	probeWorker := &worker.Worker{
		Client:       client,
		Credentials:  credentialProvider(config),
		Executor:     probe.NewDispatcher(httpExecutor, tcpExecutor, dnsExecutor),
		Capabilities: config.capabilities,
		Version:      version,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if config.kubernetesDiscovery.enabled {
		go func() {
			if err := runKubernetesDiscovery(ctx, config, client); err != nil && ctx.Err() == nil {
				slog.Warn("Kubernetes discovery stopped", "error", err)
			}
		}()
	}
	for ctx.Err() == nil {
		if err := probeWorker.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("agent iteration failed", "error", err)
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
	}
	return nil
}

func credentialProvider(config config) credentials.Provider {
	return credentials.FileProvider{Path: config.credentialFile}
}

func loadConfig(getenv func(string) string) (config, error) {
	rawURL := strings.TrimSpace(getenv("XISNOVE_URL"))
	controlPlaneURL, err := url.Parse(rawURL)
	if err != nil ||
		(controlPlaneURL.Scheme != "http" && controlPlaneURL.Scheme != "https") ||
		controlPlaneURL.Host == "" {
		return config{}, errors.New("XISNOVE_URL must be an absolute HTTP or HTTPS URL")
	}

	credentialFile := strings.TrimSpace(getenv("XISNOVE_AGENT_CREDENTIAL_FILE"))
	if credentialFile == "" {
		return config{}, errors.New("XISNOVE_AGENT_CREDENTIAL_FILE is required")
	}

	var allowedPrivate []netip.Prefix
	for _, rawPrefix := range strings.Split(
		getenv("XISNOVE_AGENT_ALLOWED_PRIVATE_CIDRS"),
		",",
	) {
		rawPrefix = strings.TrimSpace(rawPrefix)
		if rawPrefix == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(rawPrefix)
		if err != nil {
			return config{}, fmt.Errorf(
				"parse XISNOVE_AGENT_ALLOWED_PRIVATE_CIDRS: %w",
				err,
			)
		}
		allowedPrivate = append(allowedPrivate, prefix.Masked())
	}
	capabilities, err := parseCapabilities(getenv("XISNOVE_AGENT_CAPABILITIES"))
	if err != nil {
		return config{}, err
	}

	kubernetesDiscovery, err := parseKubernetesDiscovery(getenv, capabilities)
	if err != nil {
		return config{}, err
	}

	return config{
		controlPlaneURL:     strings.TrimRight(controlPlaneURL.String(), "/"),
		credentialFile:      credentialFile,
		allowedPrivate:      allowedPrivate,
		capabilities:        capabilities,
		kubernetesDiscovery: kubernetesDiscovery,
	}, nil
}

func parseKubernetesDiscovery(getenv func(string) string, capabilities []controlplane.AgentCapability) (kubernetesDiscoveryConfig, error) {
	hasCapability := false
	watch := false
	for _, capability := range capabilities {
		if capability == controlplane.AgentCapabilityKubernetesDiscovery || capability == controlplane.AgentCapabilityKubernetesWatch {
			hasCapability = true
		}
		if capability == controlplane.AgentCapabilityKubernetesWatch {
			watch = true
		}
	}
	namespaces := splitCSV(getenv("XISNOVE_DISCOVERY_NAMESPACES"))
	resources := splitCSV(getenv("XISNOVE_DISCOVERY_RESOURCES"))
	if !hasCapability || len(namespaces) == 0 || len(resources) == 0 {
		return kubernetesDiscoveryConfig{}, nil
	}
	parsed := make([]kubernetesdiscovery.Resource, 0, len(resources))
	seen := map[kubernetesdiscovery.Resource]struct{}{}
	for _, value := range resources {
		resource := kubernetesdiscovery.Resource(value)
		switch resource {
		case kubernetesdiscovery.ResourceServices, kubernetesdiscovery.ResourceEndpointSlices, kubernetesdiscovery.ResourceIngresses, kubernetesdiscovery.ResourceGateways, kubernetesdiscovery.ResourceHTTPRoutes, kubernetesdiscovery.ResourceGRPCRoutes:
		default:
			return kubernetesDiscoveryConfig{}, fmt.Errorf("XISNOVE_DISCOVERY_RESOURCES contains invalid value %q", value)
		}
		if _, duplicate := seen[resource]; !duplicate {
			seen[resource] = struct{}{}
			parsed = append(parsed, resource)
		}
	}
	return kubernetesDiscoveryConfig{enabled: true, watch: watch, namespaces: namespaces, resources: parsed}, nil
}

func splitCSV(raw string) []string {
	seen := map[string]struct{}{}
	var values []string
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; !duplicate {
			seen[value] = struct{}{}
			values = append(values, value)
		}
	}
	return values
}

func runKubernetesDiscovery(ctx context.Context, config config, client *controlplane.ClientWithResponses) error {
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("load in-cluster Kubernetes config: %w", err)
	}
	kubeClient, err := clientset.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("create Kubernetes client: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("create Kubernetes dynamic client: %w", err)
	}
	source := kubernetesdiscovery.Source{Core: kubeClient.CoreV1(), Discovery: kubeClient.DiscoveryV1(), Networking: kubeClient.NetworkingV1(), Dynamic: dynamicClient, Namespaces: config.kubernetesDiscovery.namespaces, Resources: config.kubernetesDiscovery.resources, Perspective: "kubernetes:in-cluster", OnDiagnostic: func(diagnostic kubernetesdiscovery.Diagnostic) {
		slog.Warn("Kubernetes discovery diagnostic", "code", diagnostic.Code, "source", diagnostic.Source.Kind+"/"+diagnostic.Source.Namespace+"/"+diagnostic.Source.Name)
	}}
	publisher := discovery.APIPublisher{Client: client, Credentials: credentialProvider(config)}
	runner := discovery.Runner{Producer: source, Publisher: publisher}
	if err := runner.RunOnce(ctx); err != nil {
		return fmt.Errorf("publish initial Kubernetes discovery snapshot: %w", err)
	}
	if config.kubernetesDiscovery.watch {
		watchers, err := source.Watchers(publisher)
		if err != nil {
			return fmt.Errorf("configure Kubernetes discovery watchers: %w", err)
		}
		for _, watcher := range watchers {
			go func(watcher kubernetesdiscovery.Watcher) {
				if err := watcher.Run(ctx); err != nil && ctx.Err() == nil {
					slog.Warn("Kubernetes discovery watcher stopped", "error", err)
				}
			}(watcher)
		}
	}
	return runner.Run(ctx, discovery.LoopConfig{Enabled: true, InitialDelay: true, Cadence: 5 * time.Minute, MinBackoff: time.Second, MaxBackoff: time.Minute, OnError: func(err error) { slog.Warn("Kubernetes discovery cycle failed", "error", err) }})
}

func parseCapabilities(raw string) ([]controlplane.AgentCapability, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "http,tcp,dns"
	}
	capabilities := make([]controlplane.AgentCapability, 0, 3)
	seen := make(map[controlplane.AgentCapability]struct{}, 3)
	for _, value := range strings.Split(raw, ",") {
		capability := controlplane.AgentCapability(strings.ToLower(strings.TrimSpace(value)))
		if !capability.Valid() {
			return nil, fmt.Errorf("XISNOVE_AGENT_CAPABILITIES contains invalid value %q", value)
		}
		if _, duplicate := seen[capability]; duplicate {
			continue
		}
		seen[capability] = struct{}{}
		capabilities = append(capabilities, capability)
	}
	if len(capabilities) == 0 {
		return nil, errors.New("XISNOVE_AGENT_CAPABILITIES must not be empty")
	}
	return capabilities, nil
}
