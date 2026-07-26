package credential_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func (m *memoryKeyring) Delete(service, account string) error {
	delete(m.values, service+"/"+account)
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

func TestResolverRejectsNonRegularTokenFilesWithoutDeletingThem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token-directory")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	ref := config.CredentialRef{Mode: config.CredentialFile, Reference: path}
	resolver := credential.Resolver{}

	if _, err := resolver.Lookup(ref); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("Lookup() error = %v, want regular-file rejection", err)
	}
	if err := resolver.Delete(ref); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("Delete() error = %v, want regular-file rejection", err)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("token directory was removed or replaced: info=%v err=%v", info, err)
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

func TestResolverDeletesWritableCredentials(t *testing.T) {
	ring := &memoryKeyring{values: map[string]string{"xisnove-cli/prod": "token"}}
	resolver := credential.Resolver{Keyring: ring}
	if err := resolver.Delete(config.CredentialRef{Mode: config.CredentialKeyring, Reference: "prod"}); err != nil {
		t.Fatalf("Delete(keyring) error = %v", err)
	}
	if _, ok := ring.values["xisnove-cli/prod"]; ok {
		t.Fatal("keyring credential still exists")
	}
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("token\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := resolver.Delete(config.CredentialRef{Mode: config.CredentialFile, Reference: path}); err != nil {
		t.Fatalf("Delete(file) error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token file still exists: %v", err)
	}
	if err := resolver.Delete(config.CredentialRef{Mode: config.CredentialEnv, Reference: "TOKEN"}); !errors.Is(err, credential.ErrReadOnlyMode) {
		t.Fatalf("Delete(env) error = %v, want ErrReadOnlyMode", err)
	}
}
