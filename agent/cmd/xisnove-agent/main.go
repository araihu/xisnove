package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/araihu/xisnove/agent/credentials"
	"github.com/araihu/xisnove/agent/discovery"
	kubernetesdiscovery "github.com/araihu/xisnove/agent/discovery/kubernetes"
	"github.com/araihu/xisnove/agent/internal/buildinfo"
	"github.com/araihu/xisnove/agent/internal/controlplane"
	"github.com/araihu/xisnove/agent/internal/observability"
	"github.com/araihu/xisnove/agent/probe"
	"github.com/araihu/xisnove/agent/worker"
	"k8s.io/client-go/dynamic"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type config struct {
	controlPlaneURL      string
	credentialFile       string
	observabilityAddress string
	allowedPrivate       []netip.Prefix
	capabilities         []controlplane.AgentCapability
	kubernetesDiscovery  kubernetesDiscoveryConfig
}

type kubernetesDiscoveryConfig struct {
	enabled    bool
	watch      bool
	namespaces []string
	resources  []kubernetesdiscovery.Resource
}

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr, os.Getenv, run))
}

func execute(args []string, stdout, stderr io.Writer, getenv func(string) string, runtime func(config) error) int {
	if len(args) == 1 && args[0] == "--version" {
		value, err := buildinfo.String("xisnove-agent")
		if err != nil {
			fmt.Fprintln(stderr, "xisnove-agent: invalid release metadata")
			return 2
		}
		fmt.Fprintln(stdout, value)
		return 0
	}
	if len(args) > 0 && args[0] == "enroll" {
		err := enrollCommand(context.Background(), args[1:])
		if err == nil {
			return 0
		}
		var usage *enrollUsageError
		if errors.As(err, &usage) {
			fmt.Fprintf(stderr, "invalid enrollment command: %v\n", usage)
			return 2
		}
		fmt.Fprintf(stderr, "Agent enrollment failed: %v\n", err)
		return 1
	}
	if len(args) != 0 {
		fmt.Fprintln(stderr, "usage: xisnove-agent [--version] | enroll [flags]")
		return 2
	}
	config, err := loadConfig(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "invalid Agent configuration: %v\n", err)
		return 2
	}
	if err := runtime(config); err != nil {
		fmt.Fprintf(stderr, "agent stopped: %v\n", err)
		return 1
	}
	return 0
}

func run(config config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runWithContext(ctx, config, observability.NewState())
}

func runWithContext(ctx context.Context, config config, state *observability.State) error {
	observabilityServer, err := observability.Listen(config.observabilityAddress, state)
	if err != nil {
		return err
	}
	observabilityCtx, stopObservability := context.WithCancel(context.Background())
	observabilityDone := make(chan error, 1)
	go func() { observabilityDone <- observabilityServer.Serve(observabilityCtx) }()
	shutdownObservability := func() error {
		state.BeginDrain()
		stopObservability()
		return <-observabilityDone
	}

	provider := credentialProvider(config)
	if _, err := provider.Current(ctx); err != nil {
		_ = shutdownObservability()
		return fmt.Errorf("load Agent credential: %w", err)
	}
	state.MarkCredentialLoaded()

	client, err := controlplane.NewClientWithResponses(config.controlPlaneURL)
	if err != nil {
		_ = shutdownObservability()
		return fmt.Errorf("create control-plane client: %w", err)
	}
	state.MarkClientInitialized()

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
	agentVersion := buildinfo.Version
	if agentVersion == "" {
		agentVersion = "dev"
	}
	probeWorker := &worker.Worker{
		Client:       client,
		Credentials:  provider,
		Executor:     probe.NewDispatcher(httpExecutor, tcpExecutor, dnsExecutor),
		Capabilities: config.capabilities,
		Version:      agentVersion,
	}

	workCtx, stopWork := context.WithCancel(context.Background())
	defer stopWork()
	watchDone := make(chan struct{})
	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			state.BeginDrain()
			stopWork()
		case <-stopWatch:
		}
	}()
	state.SetAcceptingLeases(true)
	if config.kubernetesDiscovery.enabled {
		go func() {
			if err := runKubernetesDiscovery(workCtx, config, client); err != nil && workCtx.Err() == nil {
				slog.Warn("Kubernetes discovery stopped", "error", err)
			}
		}()
	}
	for workCtx.Err() == nil {
		select {
		case err := <-observabilityDone:
			state.BeginDrain()
			stopWork()
			return err
		default:
		}
		_, err := provider.Current(workCtx)
		if err == nil {
			state.SetAcceptingLeases(true)
			err = probeWorker.RunOnce(workCtx)
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			state.SetAcceptingLeases(false)
			slog.Warn("agent iteration failed", "error", err)
			timer := time.NewTimer(time.Second)
			select {
			case <-workCtx.Done():
				timer.Stop()
			case err := <-observabilityDone:
				timer.Stop()
				state.BeginDrain()
				stopWork()
				return err
			case <-timer.C:
			}
		}
	}
	state.BeginDrain()
	stopWork()
	<-watchDone
	return shutdownObservability()
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
	observabilityAddress := strings.TrimSpace(getenv("XISNOVE_AGENT_OBSERVABILITY_ADDRESS"))
	if observabilityAddress == "" {
		observabilityAddress = "127.0.0.1:9090"
	}
	host, rawPort, err := net.SplitHostPort(observabilityAddress)
	port, portErr := strconv.Atoi(rawPort)
	if err != nil || portErr != nil || port < 1 || port > 65535 {
		return config{}, errors.New("XISNOVE_AGENT_OBSERVABILITY_ADDRESS must be an IP address and TCP port")
	}
	if _, err := netip.ParseAddr(host); err != nil {
		return config{}, errors.New("XISNOVE_AGENT_OBSERVABILITY_ADDRESS must be an IP address and TCP port")
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
		controlPlaneURL:      strings.TrimRight(controlPlaneURL.String(), "/"),
		credentialFile:       credentialFile,
		observabilityAddress: observabilityAddress,
		allowedPrivate:       allowedPrivate,
		capabilities:         capabilities,
		kubernetesDiscovery:  kubernetesDiscovery,
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
	if err := runner.RunUntilSuccess(ctx, discovery.LoopConfig{MinBackoff: time.Second, MaxBackoff: time.Minute, OnError: func(err error) { slog.Warn("initial Kubernetes discovery failed", "error", err) }}); err != nil {
		return fmt.Errorf("publish initial Kubernetes discovery snapshot: %w", err)
	}
	if ctx.Err() != nil {
		return nil
	}
	if config.kubernetesDiscovery.watch {
		watchers, err := source.Watchers(publisher)
		if err != nil {
			return fmt.Errorf("configure Kubernetes discovery watchers: %w", err)
		}
		relistRequests := make(chan struct{}, 1)
		go func() {
			if err := (kubernetesdiscovery.RelistCoordinator{Source: source, Publish: publisher, Requests: relistRequests}).Run(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("Kubernetes relist coordinator stopped", "error", err)
			}
		}()
		for _, watcher := range watchers {
			watcher.RelistRequests = relistRequests
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
