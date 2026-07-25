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

	"github.com/araihu/xisnove/agent/internal/controlplane"
	"github.com/araihu/xisnove/agent/probe"
	"github.com/araihu/xisnove/agent/worker"
)

var version = "dev"

type config struct {
	controlPlaneURL string
	credentialFile  string
	allowedPrivate  []netip.Prefix
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
	probeWorker := &worker.Worker{
		Client: client,
		Credential: func() (string, error) {
			contents, err := os.ReadFile(config.credentialFile)
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(string(contents)), nil
		},
		Executor:             probe.NewHTTPExecutor(policy),
		Version:              version,
		CredentialGeneration: 1,
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

	return config{
		controlPlaneURL: strings.TrimRight(controlPlaneURL.String(), "/"),
		credentialFile:  credentialFile,
		allowedPrivate:  allowedPrivate,
	}, nil
}
