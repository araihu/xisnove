package credential_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/araihu/xisnove/cli/internal/config"
	"github.com/araihu/xisnove/cli/internal/credential"
)

type memoryKeyring struct {
	values map[string]string
}

func (m *memoryKeyring) Get(service, account string) (string, error) {
	value, ok := m.values[service+"/"+account]
	if !ok {
		return "", credential.ErrCredentialNotFound
	}
	return value, nil
}

func (m *memoryKeyring) Set(service, account, value string) error {
	if m.values == nil {
		m.values = map[string]string{}
	}
	m.values[service+"/"+account] = value
	return nil
}

func TestResolverUsesSelectedCredentialModeWithoutFallback(t *testing.T) {
	ring := &memoryKeyring{values: map[string]string{"xisnove-cli/prod": "keyring-token"}}
	envReads := 0
	resolver := credential.Resolver{
		Keyring: ring,
		LookupEnv: func(name string) (string, bool) {
			envReads++
			return "environment-token", true
		},
	}

	got, err := resolver.Lookup(config.CredentialRef{Mode: config.CredentialKeyring, Reference: "prod"})
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if got != "keyring-token" {
		t.Fatalf("Lookup() = %q, want keyring token", got)
	}
	if envReads != 0 {
		t.Fatalf("environment reads = %d, want 0", envReads)
	}

	got, err = resolver.Lookup(config.CredentialRef{Mode: config.CredentialEnv, Reference: "XISNOVE_TOKEN"})
	if err != nil {
		t.Fatalf("Lookup(env) error = %v", err)
	}
	if got != "environment-token" {
		t.Fatalf("Lookup(env) = %q, want environment token", got)
	}
}

func TestResolverRejectsBroadTokenFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("secret-token\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := (credential.Resolver{}).Lookup(config.CredentialRef{Mode: config.CredentialFile, Reference: path})
	if !errors.Is(err, credential.ErrInsecurePermissions) {
		t.Fatalf("Lookup() error = %v, want ErrInsecurePermissions", err)
	}
}

func TestResolverStoresFileTokenPrivatelyAndTrimsReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "token")
	resolver := credential.Resolver{}
	ref := config.CredentialRef{Mode: config.CredentialFile, Reference: path}

	if err := resolver.Store(ref, "  token-value  \n"); err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("token permissions = %#o, want 0600", got)
	}
	got, err := resolver.Lookup(ref)
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if got != "token-value" {
		t.Fatalf("Lookup() = %q, want trimmed token", got)
	}
}

func TestResolverNeverWritesEnvironmentCredential(t *testing.T) {
	err := (credential.Resolver{}).Store(config.CredentialRef{Mode: config.CredentialEnv, Reference: "XISNOVE_TOKEN"}, "secret")
	if !errors.Is(err, credential.ErrReadOnlyMode) {
		t.Fatalf("Store() error = %v, want ErrReadOnlyMode", err)
	}
}

func TestDefaultReferencePrefersOSKeyring(t *testing.T) {
	got := credential.DefaultReference("production")
	want := config.CredentialRef{Mode: config.CredentialKeyring, Reference: "production"}
	if got != want {
		t.Fatalf("DefaultReference() = %#v, want %#v", got, want)
	}
}
