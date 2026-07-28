package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/xisnove/ui/internal/buildinfo"
	"github.com/araihu/xisnove/ui/internal/controlplane"
)

func TestExecuteVersionSkipsConfigurationAndServer(t *testing.T) {
	setUIBuildInfo(t, "1.2.3", "0123456789abcdef0123456789abcdef01234567", "2026-07-27T03:04:05Z", "false")
	var stdout, stderr bytes.Buffer
	called := false
	exit := execute([]string{"--version"}, &stdout, &stderr, func() error {
		called = true
		return nil
	})
	if exit != 0 || called || stderr.Len() != 0 {
		t.Fatalf("execute = exit %d called %t stderr %q", exit, called, stderr.String())
	}
	want := "xisnove-ui version=1.2.3 commit=0123456789abcdef0123456789abcdef01234567 build_date=2026-07-27T03:04:05Z dirty=false\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestExecuteInvalidVersionAndMalformedFlagsUseSingleUsageDiagnostic(t *testing.T) {
	tests := [][]string{{"--version"}, {"--version", "extra"}, {"--unknown"}}
	for _, arguments := range tests {
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			setUIBuildInfo(t, "dev", "bad", "bad", "true")
			var stdout, stderr bytes.Buffer
			exit := execute(arguments, &stdout, &stderr, func() error {
				t.Fatal("configuration initialized")
				return nil
			})
			if exit != 2 || stdout.Len() != 0 || strings.Count(stderr.String(), "\n") != 1 {
				t.Fatalf("execute = exit %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
			}
		})
	}
}

func setUIBuildInfo(t *testing.T, version, commit, date, dirty string) {
	t.Helper()
	oldVersion, oldCommit, oldDate, oldDirty := buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate, buildinfo.Dirty
	buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate, buildinfo.Dirty = version, commit, date, dirty
	t.Cleanup(func() {
		buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate, buildinfo.Dirty = oldVersion, oldCommit, oldDate, oldDirty
	})
}

func TestLoadConfigRequiresProductionAPIBaseURL(t *testing.T) {
	env := testEnv(map[string]string{
		"XISNOVE_UI_COOKIE_SECRET": base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
	})

	_, err := loadConfig(env)
	if err == nil || !strings.Contains(err.Error(), "XISNOVE_UI_API_BASE_URL") {
		t.Fatalf("load config error = %v, want API base URL validation", err)
	}
}

func TestRuntimeHandlerServesProbesAndPreservesApplicationRoutes(t *testing.T) {
	var lifecycle atomic.Int32
	applicationCalls := 0
	handler := runtimeHandler(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		applicationCalls++
		response.Header().Set("X-Application", "preserved")
		response.WriteHeader(http.StatusTeapot)
	}), &lifecycle)

	for _, path := range []string{"/livez", "/readyz"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusServiceUnavailable || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("starting %s = status %d cache %q", path, response.Code, response.Header().Get("Cache-Control"))
		}
	}

	lifecycle.Store(1)
	for _, path := range []string{"/livez", "/readyz"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
			t.Fatalf("ready %s = status %d body %q", path, response.Code, response.Body.String())
		}
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/monitors", nil))
	if response.Code != http.StatusTeapot || response.Header().Get("X-Application") != "preserved" || applicationCalls != 1 {
		t.Fatalf("application route = status %d header %q calls %d", response.Code, response.Header().Get("X-Application"), applicationCalls)
	}

	lifecycle.Store(2)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("draining readiness = %d", response.Code)
	}
}

func TestLoadConfigPrefersOwnerOnlyCookieSecretFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookie-secret")
	encoded := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	if err := os.WriteFile(path, []byte(encoded+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := testEnv(map[string]string{
		"XISNOVE_UI_COOKIE_SECRET_FILE": path,
		"XISNOVE_UI_DEV_FAKE":           "true",
		"XISNOVE_UI_DEV_ADMIN_EMAIL":    "local-admin@example.test",
		"XISNOVE_UI_DEV_ADMIN_PASSWORD": "server-side-password",
		"XISNOVE_UI_DEV_SESSION":        "server-side-session",
	})
	cfg, err := loadConfig(env)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if string(cfg.cookieSecret) != "0123456789abcdef0123456789abcdef" {
		t.Fatal("cookie secret file not loaded")
	}
}

func TestLoadConfigAcceptsProjectedReadOnlyCookieSecret(t *testing.T) {
	target := filepath.Join(t.TempDir(), "cookie-secret")
	encoded := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	if err := os.WriteFile(target, []byte(encoded+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "projected-cookie-secret")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	secret, err := loadCookieSecret(testEnv(map[string]string{"XISNOVE_UI_COOKIE_SECRET_FILE": link}))
	if err != nil {
		t.Fatal(err)
	}
	if string(secret) != "0123456789abcdef0123456789abcdef" {
		t.Fatal("projected cookie secret not loaded")
	}
}

func TestLoadConfigRejectsAmbiguousOrUnsafeCookieSecretFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "cookie-secret")
	encoded := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	if err := os.WriteFile(path, []byte(encoded), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, values := range []map[string]string{
		{
			"XISNOVE_UI_COOKIE_SECRET":      encoded,
			"XISNOVE_UI_COOKIE_SECRET_FILE": path,
		},
		{"XISNOVE_UI_COOKIE_SECRET_FILE": path},
	} {
		_, err := loadConfig(testEnv(values))
		if err == nil {
			t.Fatal("loadConfig error = nil")
		}
		if strings.Contains(err.Error(), path) || strings.Contains(err.Error(), encoded) {
			t.Fatalf("error leaks secret source: %v", err)
		}
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
