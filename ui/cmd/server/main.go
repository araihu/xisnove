package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/araihu/xisnove/sdk"
	"github.com/araihu/xisnove/ui/internal/controlplane"
	"github.com/araihu/xisnove/ui/internal/web"
	"github.com/google/uuid"
)

type config struct {
	addr            string
	cookieSecret    []byte
	cookieSecure    bool
	requestTimeout  time.Duration
	shutdownTimeout time.Duration
	controlPlane    controlplane.Client
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		logger.Error("configuration failed", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg, logger); err != nil {
		logger.Error("UI server stopped", "error", err)
		os.Exit(1)
	}
}

func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		addr:            "127.0.0.1:8081",
		cookieSecure:    true,
		requestTimeout:  5 * time.Second,
		shutdownTimeout: 10 * time.Second,
	}
	if value := getenv("XISNOVE_UI_ADDR"); value != "" {
		cfg.addr = value
	}
	if value := getenv("XISNOVE_UI_COOKIE_SECURE"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return config{}, fmt.Errorf("parse XISNOVE_UI_COOKIE_SECURE: %w", err)
		}
		cfg.cookieSecure = parsed
	}
	if value := getenv("XISNOVE_UI_REQUEST_TIMEOUT"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			return config{}, errors.New("XISNOVE_UI_REQUEST_TIMEOUT must be a positive duration")
		}
		cfg.requestTimeout = parsed
	}
	if value := getenv("XISNOVE_UI_SHUTDOWN_TIMEOUT"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			return config{}, errors.New("XISNOVE_UI_SHUTDOWN_TIMEOUT must be a positive duration")
		}
		cfg.shutdownTimeout = parsed
	}

	secret, err := decodeSecret(getenv("XISNOVE_UI_COOKIE_SECRET"))
	if err != nil {
		return config{}, fmt.Errorf("decode XISNOVE_UI_COOKIE_SECRET: %w", err)
	}
	cfg.cookieSecret = secret

	devFake, err := strconv.ParseBool(defaultValue(getenv("XISNOVE_UI_DEV_FAKE"), "false"))
	if err != nil {
		return config{}, fmt.Errorf("parse XISNOVE_UI_DEV_FAKE: %w", err)
	}
	if devFake {
		email := getenv("XISNOVE_UI_DEV_ADMIN_EMAIL")
		password := getenv("XISNOVE_UI_DEV_ADMIN_PASSWORD")
		session := getenv("XISNOVE_UI_DEV_SESSION")
		if email == "" || password == "" || session == "" {
			return config{}, errors.New("development fake requires explicit email, password, and session values")
		}
		cfg.controlPlane = developmentFake(email, password, session)
		return cfg, nil
	}
	baseURL := getenv("XISNOVE_UI_API_BASE_URL")
	parsed, err := url.ParseRequestURI(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return config{}, errors.New("XISNOVE_UI_API_BASE_URL must be an absolute http or https URL")
	}
	client, err := controlplane.NewSDKClient(baseURL, &http.Client{Timeout: cfg.requestTimeout})
	if err != nil {
		return config{}, err
	}
	cfg.controlPlane = client
	return cfg, nil
}

func developmentFake(email, password, session string) *controlplane.Fake {
	fake := controlplane.NewFake(email, password, session)
	dnsID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	wanID := uuid.MustParse("10000000-0000-4000-8000-000000000002")
	fake.Monitors = []sdk.Monitor{
		{Id: dnsID, Name: "Home DNS", Description: "Resolver reachability from the control plane", Kind: sdk.MonitorKindDns, Enabled: true, Public: true},
		{Id: wanID, Name: "VPS edge", Description: "External HTTP ingress", Kind: sdk.MonitorKindHttp, Enabled: true, Public: true},
	}
	fake.Health[dnsID] = sdk.MonitorHealth{MonitorId: dnsID, State: sdk.Up}
	fake.Health[wanID] = sdk.MonitorHealth{MonitorId: wanID, State: sdk.Unknown}
	fake.PublicStatus = sdk.PublicStatusPage{GeneratedAt: time.Now().UTC(), State: sdk.Degraded, Monitors: []sdk.PublicStatusMonitor{
		{Id: dnsID, Name: "Home DNS", Description: "Resolver reachability", State: sdk.Up},
		{Id: wanID, Name: "VPS edge", Description: "External HTTP ingress", State: sdk.Unknown},
	}}
	return fake
}

func decodeSecret(value string) ([]byte, error) {
	if value == "" {
		return nil, errors.New("value is required")
	}
	secret, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		secret, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil {
		return nil, errors.New("value must be standard base64")
	}
	if len(secret) < 32 {
		return nil, errors.New("decoded value must contain at least 32 bytes")
	}
	return secret, nil
}

func defaultValue(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func run(ctx context.Context, cfg config, logger *slog.Logger) error {
	handler, err := web.New(web.Config{
		ControlPlane:   cfg.controlPlane,
		CookieSecret:   cfg.cookieSecret,
		CookieSecure:   cfg.cookieSecure,
		RequestTimeout: cfg.requestTimeout,
		Logger:         logger,
	})
	if err != nil {
		return fmt.Errorf("create UI handler: %w", err)
	}
	httpServer := &http.Server{
		Addr:              cfg.addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      cfg.requestTimeout + 5*time.Second,
		IdleTimeout:       60 * time.Second,
	}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.shutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
	}()

	logger.Info("UI server listening", "address", cfg.addr, "secure_cookie", cfg.cookieSecure)
	err = httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		<-shutdownDone
		return nil
	}
	return err
}
