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
	"github.com/araihu/xisnove/agent/internal/controlplane"
	"github.com/araihu/xisnove/agent/probe"
	"github.com/araihu/xisnove/agent/worker"
)

var version = "dev"

type config struct {
	controlPlaneURL string
	credentialFile  string
	allowedPrivate  []netip.Prefix
	capabilities    []controlplane.AgentCapability
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

	return config{
		controlPlaneURL: strings.TrimRight(controlPlaneURL.String(), "/"),
		credentialFile:  credentialFile,
		allowedPrivate:  allowedPrivate,
		capabilities:    capabilities,
	}, nil
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
