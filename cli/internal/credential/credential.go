package credential

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/araihu/xisnove/cli/internal/config"
	keyring "github.com/zalando/go-keyring"
)

const defaultService = "xisnove-cli"

var (
	ErrCredentialNotFound  = errors.New("credential not found")
	ErrInsecurePermissions = errors.New("insecure credential permissions")
	ErrReadOnlyMode        = errors.New("credential mode is read-only")
)

type Keyring interface {
	Get(service, account string) (string, error)
	Set(service, account, value string) error
}

type osKeyring struct{}

func (osKeyring) Get(service, account string) (string, error) {
	value, err := keyring.Get(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrCredentialNotFound
	}
	return value, err
}

func (osKeyring) Set(service, account, value string) error {
	return keyring.Set(service, account, value)
}

type Resolver struct {
	Keyring   Keyring
	LookupEnv func(string) (string, bool)
	Service   string
}

func DefaultReference(profile string) config.CredentialRef {
	return config.CredentialRef{Mode: config.CredentialKeyring, Reference: profile}
}

func (r Resolver) Lookup(ref config.CredentialRef) (string, error) {
	switch ref.Mode {
	case config.CredentialKeyring:
		value, err := r.keyring().Get(r.service(), ref.Reference)
		if err != nil {
			return "", fmt.Errorf("read keyring credential %q: %w", ref.Reference, err)
		}
		return requireToken(value, ref.Reference)
	case config.CredentialEnv:
		value, ok := r.lookupEnv()(ref.Reference)
		if !ok {
			return "", fmt.Errorf("read environment credential %q: %w", ref.Reference, ErrCredentialNotFound)
		}
		return requireToken(value, ref.Reference)
	case config.CredentialFile:
		return readFile(ref.Reference)
	default:
		return "", fmt.Errorf("unsupported credential mode %q", ref.Mode)
	}
}

func (r Resolver) Store(ref config.CredentialRef, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("credential must not be empty")
	}
	switch ref.Mode {
	case config.CredentialKeyring:
		if err := r.keyring().Set(r.service(), ref.Reference, value); err != nil {
			return fmt.Errorf("write keyring credential %q: %w", ref.Reference, err)
		}
		return nil
	case config.CredentialFile:
		return writeFile(ref.Reference, value)
	case config.CredentialEnv:
		return fmt.Errorf("environment credential %q: %w", ref.Reference, ErrReadOnlyMode)
	default:
		return fmt.Errorf("unsupported credential mode %q", ref.Mode)
	}
}

func (r Resolver) keyring() Keyring {
	if r.Keyring != nil {
		return r.Keyring
	}
	return osKeyring{}
}

func (r Resolver) lookupEnv() func(string) (string, bool) {
	if r.LookupEnv != nil {
		return r.LookupEnv
	}
	return os.LookupEnv
}

func (r Resolver) service() string {
	if r.Service != "" {
		return r.Service
	}
	return defaultService
}

func requireToken(value, reference string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("credential %q is empty: %w", reference, ErrCredentialNotFound)
	}
	return value, nil
}

func readFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read token file %q: %w", path, ErrCredentialNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("inspect token file %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("token file %q must not be a symbolic link", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%w: token file %s is %#o; run chmod 600 %s", ErrInsecurePermissions, path, info.Mode().Perm(), path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read token file %q: %w", path, err)
	}
	return requireToken(string(data), path)
}

func writeFile(path, value string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create token directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return fmt.Errorf("create temporary token file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure temporary token file: %w", err)
	}
	if _, err := tmp.WriteString(value + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary token file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temporary token file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary token file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace token file: %w", err)
	}
	return nil
}
