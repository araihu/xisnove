package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/araihu/xisnove/cli/internal/config"
)

func TestStoreRoundTripUsesPrivateDeterministicFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	store := config.Store{Path: path}
	want := config.Config{
		Version:        1,
		CurrentProfile: "production",
		Profiles: map[string]config.Profile{
			"production": {
				URL:        "https://xisnove.example.com",
				Credential: config.CredentialRef{Mode: config.CredentialKeyring, Reference: "production"},
			},
			"automation": {
				URL:        "https://automation.example.com",
				Credential: config.CredentialRef{Mode: config.CredentialEnv, Reference: "XISNOVE_AUTOMATION_TOKEN"},
			},
		},
	}

	if err := store.Save(want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config permissions = %#o, want 0600", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	wantYAML := "version: 1\ncurrentProfile: production\nprofiles:\n    automation:\n        url: https://automation.example.com\n        credential:\n            mode: env\n            reference: XISNOVE_AUTOMATION_TOKEN\n    production:\n        url: https://xisnove.example.com\n        credential:\n            mode: keyring\n            reference: production\n"
	if got := string(data); got != wantYAML {
		t.Fatalf("config YAML mismatch\n--- got ---\n%s--- want ---\n%s", got, wantYAML)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.CurrentProfile != want.CurrentProfile || len(got.Profiles) != len(want.Profiles) {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
}

func TestStoreRejectsConfigReadableByOtherUsers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "version: 1\nprofiles: {}\n"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := (config.Store{Path: path}).Load()
	if !errors.Is(err, config.ErrInsecurePermissions) {
		t.Fatalf("Load() error = %v, want ErrInsecurePermissions", err)
	}
	if err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("Load() error = %v, want remediation", err)
	}
}

func TestStoreRejectsNonRegularConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	_, err := (config.Store{Path: path}).Load()
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("Load() error = %v, want regular-file rejection", err)
	}
}

func TestProfileValidationRejectsUnusableReferences(t *testing.T) {
	tests := []struct {
		name    string
		profile config.Profile
	}{
		{name: "non HTTP URL", profile: config.Profile{URL: "ssh://example.com", Credential: config.CredentialRef{Mode: config.CredentialKeyring, Reference: "prod"}}},
		{name: "missing env name", profile: config.Profile{URL: "https://example.com", Credential: config.CredentialRef{Mode: config.CredentialEnv}}},
		{name: "relative token file", profile: config.Profile{URL: "https://example.com", Credential: config.CredentialRef{Mode: config.CredentialFile, Reference: "token"}}},
		{name: "plaintext remote URL", profile: config.Profile{URL: "http://example.com", Credential: config.CredentialRef{Mode: config.CredentialKeyring, Reference: "prod"}}},
		{name: "URL embedded credentials", profile: config.Profile{URL: "https://admin:secret@example.com", Credential: config.CredentialRef{Mode: config.CredentialKeyring, Reference: "prod"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{Version: 1, CurrentProfile: "prod", Profiles: map[string]config.Profile{"prod": tt.profile}}
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want validation error")
			}
		})
	}
}

func TestProfileValidationAllowsPlainHTTPOnlyForLoopback(t *testing.T) {
	for _, rawURL := range []string{"http://127.0.0.1:8080", "http://[::1]:8080", "http://localhost:8080"} {
		cfg := config.Config{
			Version:        1,
			CurrentProfile: "local",
			Profiles: map[string]config.Profile{
				"local": {URL: rawURL, Credential: config.CredentialRef{Mode: config.CredentialEnv, Reference: "XISNOVE_TOKEN"}},
			},
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate(%q) error = %v", rawURL, err)
		}
	}
}
