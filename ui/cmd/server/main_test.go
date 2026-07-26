package main

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/araihu/xisnove/ui/internal/controlplane"
)

func TestLoadConfigRequiresProductionAPIBaseURL(t *testing.T) {
	env := testEnv(map[string]string{
		"XISNOVE_UI_COOKIE_SECRET": base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
	})

	_, err := loadConfig(env)
	if err == nil || !strings.Contains(err.Error(), "XISNOVE_UI_API_BASE_URL") {
		t.Fatalf("load config error = %v, want API base URL validation", err)
	}
}

func TestLoadConfigBuildsProductionSDKClient(t *testing.T) {
	env := testEnv(map[string]string{
		"XISNOVE_UI_COOKIE_SECRET":   base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
		"XISNOVE_UI_API_BASE_URL":    "https://control.example.test",
		"XISNOVE_UI_REQUEST_TIMEOUT": "2s",
	})
	cfg, err := loadConfig(env)
	if err != nil {
		t.Fatalf("load production config: %v", err)
	}
	if cfg.controlPlane == nil || cfg.requestTimeout != 2*time.Second {
		t.Fatalf("production config = %#v", cfg)
	}
}

func TestLoadConfigUsesSecureBoundedDefaultsAndServerSideFake(t *testing.T) {
	env := testEnv(map[string]string{
		"XISNOVE_UI_COOKIE_SECRET":      base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
		"XISNOVE_UI_DEV_FAKE":           "true",
		"XISNOVE_UI_DEV_ADMIN_EMAIL":    "local-admin@example.test",
		"XISNOVE_UI_DEV_ADMIN_PASSWORD": "server-side-password",
		"XISNOVE_UI_DEV_SESSION":        "server-side-session",
	})

	cfg, err := loadConfig(env)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.addr != "127.0.0.1:8081" || !cfg.cookieSecure || cfg.requestTimeout != 5*time.Second {
		t.Fatalf("defaults = addr %q secure %t timeout %v", cfg.addr, cfg.cookieSecure, cfg.requestTimeout)
	}
	credential, err := cfg.controlPlane.ExchangeAdministratorCredentials(t.Context(), "local-admin@example.test", "server-side-password")
	if err != nil || credential != "server-side-session" {
		t.Fatalf("configured fake exchange = %q, %v", credential, err)
	}
	if _, err := cfg.controlPlane.ExchangeAdministratorCredentials(t.Context(), "local-admin@example.test", "wrong"); !errors.Is(err, controlplane.ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v", err)
	}
}

func TestLoadConfigAllowsExplicitInsecureCookieForLocalHTTP(t *testing.T) {
	env := testEnv(map[string]string{
		"XISNOVE_UI_COOKIE_SECRET":      base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
		"XISNOVE_UI_COOKIE_SECURE":      "false",
		"XISNOVE_UI_REQUEST_TIMEOUT":    "750ms",
		"XISNOVE_UI_ADDR":               "127.0.0.1:0",
		"XISNOVE_UI_DEV_FAKE":           "true",
		"XISNOVE_UI_DEV_ADMIN_EMAIL":    "local-admin@example.test",
		"XISNOVE_UI_DEV_ADMIN_PASSWORD": "server-side-password",
		"XISNOVE_UI_DEV_SESSION":        "server-side-session",
	})

	cfg, err := loadConfig(env)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.cookieSecure || cfg.requestTimeout != 750*time.Millisecond || cfg.addr != "127.0.0.1:0" {
		t.Fatalf("explicit config = addr %q secure %t timeout %v", cfg.addr, cfg.cookieSecure, cfg.requestTimeout)
	}
}

func testEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}
