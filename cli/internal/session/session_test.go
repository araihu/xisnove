package session_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/araihu/xisnove/cli/internal/config"
	"github.com/araihu/xisnove/cli/internal/credential"
	"github.com/araihu/xisnove/cli/internal/session"
)

type keyring struct {
	values map[string]string
}

func (k keyring) Get(service, account string) (string, error) {
	value, ok := k.values[service+"/"+account]
	if !ok {
		return "", credential.ErrCredentialNotFound
	}
	return value, nil
}

func (keyring) Set(string, string, string) error { return errors.New("unexpected write") }
func (keyring) Delete(string, string) error      { return errors.New("unexpected delete") }

func TestResolverOpensCurrentOrExplicitProfileWithCredential(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Config{
		Version:        1,
		CurrentProfile: "production",
		Profiles: map[string]config.Profile{
			"production": {URL: "https://production.example", Credential: config.CredentialRef{Mode: config.CredentialKeyring, Reference: "production"}},
			"staging":    {URL: "https://staging.example", Credential: config.CredentialRef{Mode: config.CredentialEnv, Reference: "STAGING_TOKEN"}},
		},
	}
	if err := (config.Store{Path: configPath}).Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	resolver := session.Resolver{
		Store: config.Store{Path: configPath},
		Credentials: credential.Resolver{
			Keyring: keyring{values: map[string]string{"xisnove-cli/production": "production-token"}},
			LookupEnv: func(name string) (string, bool) {
				if name != "STAGING_TOKEN" {
					t.Fatalf("LookupEnv(%q)", name)
				}
				return "staging-token", true
			},
		},
	}

	current, err := resolver.Open("")
	if err != nil {
		t.Fatalf("Open(current) error = %v", err)
	}
	if current.Name != "production" || current.URL != "https://production.example" || current.Token != "production-token" {
		t.Fatalf("Open(current) = %#v", current)
	}
	explicit, err := resolver.Open("staging")
	if err != nil {
		t.Fatalf("Open(staging) error = %v", err)
	}
	if explicit.Name != "staging" || explicit.Token != "staging-token" {
		t.Fatalf("Open(staging) = %#v", explicit)
	}
}

func TestResolverFailsBeforeCredentialLookupWhenNoProfileSelected(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := (config.Store{Path: configPath}).Save(config.Config{Version: 1, Profiles: map[string]config.Profile{}}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	reads := 0
	resolver := session.Resolver{
		Store: config.Store{Path: configPath},
		Credentials: credential.Resolver{LookupEnv: func(string) (string, bool) {
			reads++
			return "", false
		}},
	}
	if _, err := resolver.Open(""); !errors.Is(err, session.ErrProfileNotFound) {
		t.Fatalf("Open() error = %v, want ErrProfileNotFound", err)
	}
	if reads != 0 {
		t.Fatalf("credential reads = %d, want 0", reads)
	}
}

func TestResolverOpensPublicProfileWithoutReadingCredential(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := (config.Store{Path: configPath}).Save(config.Config{
		Version:        1,
		CurrentProfile: "public",
		Profiles: map[string]config.Profile{
			"public": {URL: "https://status.example", Credential: config.CredentialRef{Mode: config.CredentialEnv, Reference: "MISSING_TOKEN"}},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	reads := 0
	resolver := session.Resolver{
		Store: config.Store{Path: configPath},
		Credentials: credential.Resolver{LookupEnv: func(string) (string, bool) {
			reads++
			return "", false
		}},
	}
	profile, err := resolver.Profile("")
	if err != nil {
		t.Fatalf("Profile() error = %v", err)
	}
	if profile.Name != "public" || profile.URL != "https://status.example" {
		t.Fatalf("Profile() = %#v", profile)
	}
	if reads != 0 {
		t.Fatalf("credential reads = %d, want 0", reads)
	}
}
