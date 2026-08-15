package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/araihu/xisnove/sdk"
	"github.com/araihu/xisnove/ui/internal/buildinfo"
	"github.com/araihu/xisnove/ui/internal/controlplane"
	"github.com/araihu/xisnove/ui/internal/web"
	"github.com/caarlos0/env/v11"
	"github.com/google/uuid"
)

type environment struct {
	Addr             string        `env:"XISNOVE_UI_ADDR" envDefault:"127.0.0.1:8081"`
	CookieSecret     string        `env:"XISNOVE_UI_COOKIE_SECRET"`
	CookieSecretFile string        `env:"XISNOVE_UI_COOKIE_SECRET_FILE"`
	CookieSecure     bool          `env:"XISNOVE_UI_COOKIE_SECURE" envDefault:"true"`
	RequestTimeout   time.Duration `env:"XISNOVE_UI_REQUEST_TIMEOUT" envDefault:"5s"`
	ShutdownTimeout  time.Duration `env:"XISNOVE_UI_SHUTDOWN_TIMEOUT" envDefault:"10s"`
	DevFake          bool          `env:"XISNOVE_UI_DEV_FAKE" envDefault:"false"`
	DevAdminEmail    string        `env:"XISNOVE_UI_DEV_ADMIN_EMAIL"`
	DevAdminPassword string        `env:"XISNOVE_UI_DEV_ADMIN_PASSWORD"`
	DevSession       string        `env:"XISNOVE_UI_DEV_SESSION"`
	APIBaseURL       string        `env:"XISNOVE_UI_API_BASE_URL"`
	AuthModes        []string      `env:"AUTH_MODES" envDefault:"basic"`
}

type config struct {
	addr            string
	cookieSecret    []byte
	cookieSecure    bool
	requestTimeout  time.Duration
	shutdownTimeout time.Duration
	authModes       []web.AuthMode
	controlPlane    controlplane.Client
}

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr, func() error {
		logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
		cfg, err := loadConfig(os.Getenv)
		if err != nil {
			return fmt.Errorf("configuration failed: %w", err)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := run(ctx, cfg, logger); err != nil {
			return fmt.Errorf("UI server stopped: %w", err)
		}
		return nil
	}))
}

func execute(args []string, stdout, stderr io.Writer, start func() error) int {
	if len(args) > 0 {
		if args[0] != "--version" || len(args) != 1 {
			fmt.Fprintln(stderr, "error: malformed flags")
			return 2
		}
		value, err := buildinfo.String("xisnove-ui")
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, value)
		return 0
	}
	if err := start(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func loadConfig(getenv func(string) string) (config, error) {
	environmentConfig, err := parseEnvironment(getenv)
	if err != nil {
		return config{}, err
	}
	authModes, err := parseAuthModes(environmentConfig.AuthModes)
	if err != nil {
		return config{}, err
	}
	cfg := config{
		addr:            environmentConfig.Addr,
		cookieSecure:    environmentConfig.CookieSecure,
		requestTimeout:  environmentConfig.RequestTimeout,
		shutdownTimeout: environmentConfig.ShutdownTimeout,
		authModes:       authModes,
	}
	secret, err := loadCookieSecret(func(key string) string {
		switch key {
		case "XISNOVE_UI_COOKIE_SECRET":
			return environmentConfig.CookieSecret
		case "XISNOVE_UI_COOKIE_SECRET_FILE":
			return environmentConfig.CookieSecretFile
		default:
			return ""
		}
	})
	if err != nil {
		return config{}, fmt.Errorf("load UI cookie secret: %w", err)
	}
	cfg.cookieSecret = secret

	if containsAuthMode(authModes, web.AuthModeNone) {
		if !environmentConfig.DevFake {
			return config{}, errors.New("AUTH_MODES=none requires XISNOVE_UI_DEV_FAKE=true")
		}
		cfg.controlPlane = developmentFake("none", "none", web.DevelopmentNoneCredential)
		return cfg, nil
	}
	if environmentConfig.DevFake {
		email := environmentConfig.DevAdminEmail
		password := environmentConfig.DevAdminPassword
		session := environmentConfig.DevSession
		if email == "" || password == "" || session == "" {
			return config{}, errors.New("development fake requires explicit email, password, and session values")
		}
		cfg.controlPlane = developmentFake(email, password, session)
		return cfg, nil
	}
	baseURL := environmentConfig.APIBaseURL
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

func parseEnvironment(getenv func(string) string) (environment, error) {
	values := make(map[string]string)
	for _, key := range []string{
		"XISNOVE_UI_ADDR",
		"XISNOVE_UI_COOKIE_SECRET",
		"XISNOVE_UI_COOKIE_SECRET_FILE",
		"XISNOVE_UI_COOKIE_SECURE",
		"XISNOVE_UI_REQUEST_TIMEOUT",
		"XISNOVE_UI_SHUTDOWN_TIMEOUT",
		"XISNOVE_UI_DEV_FAKE",
		"XISNOVE_UI_DEV_ADMIN_EMAIL",
		"XISNOVE_UI_DEV_ADMIN_PASSWORD",
		"XISNOVE_UI_DEV_SESSION",
		"XISNOVE_UI_API_BASE_URL",
		"AUTH_MODES",
	} {
		if value := getenv(key); value != "" {
			values[key] = value
		}
	}
	var parsed environment
	if err := env.ParseWithOptions(&parsed, env.Options{Environment: values}); err != nil {
		return environment{}, fmt.Errorf("parse UI environment: %w", err)
	}
	return parsed, nil
}

func parseAuthModes(values []string) ([]web.AuthMode, error) {
	if len(values) == 0 {
		values = []string{string(web.AuthModeBasic)}
	}
	seen := make(map[web.AuthMode]struct{}, len(values))
	for _, value := range values {
		mode := web.AuthMode(strings.TrimSpace(value))
		switch mode {
		case web.AuthModeBasic, web.AuthModeNone, web.AuthModeOIDC:
		default:
			return nil, fmt.Errorf("AUTH_MODES contains unsupported auth mode %q", value)
		}
		if mode == web.AuthModeOIDC {
			return nil, errors.New("OIDC authentication is not implemented")
		}
		seen[mode] = struct{}{}
	}
	if _, ok := seen[web.AuthModeNone]; ok && len(seen) > 1 {
		return nil, errors.New("none auth mode cannot be combined with another auth mode")
	}
	modes := make([]web.AuthMode, 0, len(seen))
	for _, mode := range []web.AuthMode{web.AuthModeBasic, web.AuthModeNone} {
		if _, ok := seen[mode]; ok {
			modes = append(modes, mode)
		}
	}
	return modes, nil
}

func containsAuthMode(modes []web.AuthMode, want web.AuthMode) bool {
	for _, mode := range modes {
		if mode == want {
			return true
		}
	}
	return false
}

func loadCookieSecret(getenv func(string) string) ([]byte, error) {
	direct := getenv("XISNOVE_UI_COOKIE_SECRET")
	path := getenv("XISNOVE_UI_COOKIE_SECRET_FILE")
	if direct != "" && path != "" {
		return nil, errors.New("set only one cookie secret source")
	}
	if path == "" {
		return decodeSecret(direct)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cookie secret file is unavailable")
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || openedInfo.Mode().Perm()&0o037 != 0 || openedInfo.Mode().Perm()&0o440 == 0 {
		return nil, errors.New("cookie secret file must be regular and workload-read-only")
	}
	contents, err := io.ReadAll(io.LimitReader(file, 16*1024+1))
	if err != nil {
		return nil, errors.New("cookie secret file cannot be read")
	}
	if len(contents) > 16*1024 {
		return nil, errors.New("cookie secret file is too large")
	}
	return decodeSecret(strings.TrimSpace(string(contents)))
}

func developmentFake(email, password, session string) *controlplane.Fake {
	fake := controlplane.NewFake(email, password, session)
	now := time.Now().UTC()
	dnsID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	wanID := uuid.MustParse("10000000-0000-4000-8000-000000000002")
	httpFixtureID := uuid.MustParse("10000000-0000-4000-8000-000000000003")
	tcpFixtureID := uuid.MustParse("10000000-0000-4000-8000-000000000004")
	dnsFixtureID := uuid.MustParse("10000000-0000-4000-8000-000000000005")
	fake.Monitors = []sdk.Monitor{
		{Id: dnsID, Name: "Home DNS", Description: "Resolver reachability from the control plane", Kind: sdk.MonitorKindDns, Enabled: true, Public: true},
		{Id: wanID, Name: "VPS edge", Description: "External HTTP ingress", Kind: sdk.MonitorKindHttp, Enabled: true, Public: true},
		composeHTTPMonitor(httpFixtureID, now),
		composeTCPMonitor(tcpFixtureID, now),
		composeDNSMonitor(dnsFixtureID, now),
	}
	fake.Health[dnsID] = sdk.MonitorHealth{MonitorId: dnsID, State: sdk.Up}
	fake.Health[wanID] = sdk.MonitorHealth{MonitorId: wanID, State: sdk.Unknown}
	for _, monitorID := range []uuid.UUID{httpFixtureID, tcpFixtureID, dnsFixtureID} {
		fake.Health[monitorID] = sdk.MonitorHealth{MonitorId: monitorID, State: sdk.Up, LastTransitionAt: now}
	}
	fake.PublicStatus = sdk.PublicStatusPage{GeneratedAt: time.Now().UTC(), State: sdk.Degraded, Monitors: []sdk.PublicStatusMonitor{
		{Id: dnsID, Name: "Home DNS", Description: "Resolver reachability", State: sdk.Up},
		{Id: wanID, Name: "VPS edge", Description: "External HTTP ingress", State: sdk.Unknown},
	}}
	return fake
}

func composeHTTPMonitor(id uuid.UUID, now time.Time) sdk.Monitor {
	return sdk.Monitor{
		Id:                id,
		Name:              "Compose HTTP",
		Description:       "Local HTTP fixture at monitor-http:8080/healthz",
		Kind:              sdk.MonitorKindHttp,
		Labels:            map[string]string{"environment": "compose-dev", "fixture": "monitor-http"},
		Enabled:           true,
		IntervalSeconds:   30,
		TimeoutMillis:     2000,
		FailureThreshold:  3,
		RecoveryThreshold: 2,
		Public:            false,
		DisplayOrder:      10,
		CreatedAt:         now,
		UpdatedAt:         now,
		Probe: mustDevelopmentProbe(func(probe *sdk.ProbeDefinition) error {
			return probe.FromHTTPProbeDefinition(sdk.HTTPProbeDefinition{
				Body: []byte{}, BodyContains: []string{"ok"}, BodyDoesNotContain: []string{},
				ExpectedStatus:  []sdk.StatusRange{{Minimum: 200, Maximum: 299}},
				FollowRedirects: false, Headers: map[string]string{}, Kind: sdk.HTTPProbeDefinitionKindHttp,
				Method: sdk.GET, Url: "http://monitor-http:8080/healthz",
			})
		}),
	}
}

func composeTCPMonitor(id uuid.UUID, now time.Time) sdk.Monitor {
	return sdk.Monitor{
		Id:                id,
		Name:              "Compose TCP",
		Description:       "Local TCP fixture at monitor-tcp:9090",
		Kind:              sdk.MonitorKindTcp,
		Labels:            map[string]string{"environment": "compose-dev", "fixture": "monitor-tcp"},
		Enabled:           true,
		IntervalSeconds:   30,
		TimeoutMillis:     2000,
		FailureThreshold:  3,
		RecoveryThreshold: 2,
		Public:            false,
		DisplayOrder:      11,
		CreatedAt:         now,
		UpdatedAt:         now,
		Probe: mustDevelopmentProbe(func(probe *sdk.ProbeDefinition) error {
			return probe.FromTCPProbeDefinition(sdk.TCPProbeDefinition{
				Expect: []byte("PONG"), Host: "monitor-tcp", Kind: sdk.TCPProbeDefinitionKindTcp,
				Port: 9090, Send: []byte("PING"),
			})
		}),
	}
}

func composeDNSMonitor(id uuid.UUID, now time.Time) sdk.Monitor {
	return sdk.Monitor{
		Id:                id,
		Name:              "Compose DNS",
		Description:       "Local DNS fixture at monitor-dns:5353 for service.test",
		Kind:              sdk.MonitorKindDns,
		Labels:            map[string]string{"environment": "compose-dev", "fixture": "monitor-dns"},
		Enabled:           true,
		IntervalSeconds:   30,
		TimeoutMillis:     2000,
		FailureThreshold:  3,
		RecoveryThreshold: 2,
		Public:            false,
		DisplayOrder:      12,
		CreatedAt:         now,
		UpdatedAt:         now,
		Probe: mustDevelopmentProbe(func(probe *sdk.ProbeDefinition) error {
			return probe.FromDNSProbeDefinition(sdk.DNSProbeDefinition{
				ExpectedValues: []string{"192.0.2.10"}, Kind: sdk.DNSProbeDefinitionKindDns,
				Name: "service.test", RecordType: sdk.A, Resolver: "monitor-dns:5353",
			})
		}),
	}
}

func mustDevelopmentProbe(build func(*sdk.ProbeDefinition) error) sdk.ProbeDefinition {
	var probe sdk.ProbeDefinition
	if err := build(&probe); err != nil {
		panic(fmt.Sprintf("build development monitor probe: %v", err))
	}
	return probe
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

func run(ctx context.Context, cfg config, logger *slog.Logger) error {
	application, err := web.New(web.Config{
		ControlPlane:   cfg.controlPlane,
		CookieSecret:   cfg.cookieSecret,
		CookieSecure:   cfg.cookieSecure,
		RequestTimeout: cfg.requestTimeout,
		AuthModes:      cfg.authModes,
		Logger:         logger,
	})
	if err != nil {
		return fmt.Errorf("create UI handler: %w", err)
	}
	var lifecycle atomic.Int32
	httpServer := &http.Server{
		Addr:              cfg.addr,
		Handler:           runtimeHandler(application, &lifecycle),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		// Finite handlers carry requestTimeout; availability SSE owns its
		// lifetime and must not be cut off by a connection write deadline.
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
	}
	listener, err := net.Listen("tcp", cfg.addr)
	if err != nil {
		return err
	}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		lifecycle.Store(2)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.shutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
	}()

	if !lifecycle.CompareAndSwap(0, 1) {
		_ = listener.Close()
		<-shutdownDone
		return nil
	}
	logger.Info("UI server listening", "address", cfg.addr, "secure_cookie", cfg.cookieSecure)
	err = httpServer.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		<-shutdownDone
		return nil
	}
	return err
}

func runtimeHandler(application http.Handler, lifecycle *atomic.Int32) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/livez" || request.URL.Path == "/readyz" {
			response.Header().Set("Cache-Control", "no-store")
			if lifecycle.Load() != 1 {
				response.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			response.WriteHeader(http.StatusNoContent)
			return
		}
		application.ServeHTTP(response, request)
	})
}
