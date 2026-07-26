package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

const CurrentVersion = 1

var ErrInsecurePermissions = errors.New("insecure permissions")

type CredentialMode string

const (
	CredentialKeyring CredentialMode = "keyring"
	CredentialEnv     CredentialMode = "env"
	CredentialFile    CredentialMode = "file"
)

type CredentialRef struct {
	Mode      CredentialMode `yaml:"mode" json:"mode"`
	Reference string         `yaml:"reference" json:"reference"`
}

func (r CredentialRef) Validate() error {
	if r.Reference == "" {
		return errors.New("credential reference must not be empty")
	}
	switch r.Mode {
	case CredentialKeyring, CredentialEnv:
		return nil
	case CredentialFile:
		if !filepath.IsAbs(r.Reference) {
			return errors.New("token file must be an absolute path")
		}
		return nil
	default:
		return fmt.Errorf("credential mode %q is unsupported", r.Mode)
	}
}

type Profile struct {
	URL        string        `yaml:"url" json:"url"`
	Credential CredentialRef `yaml:"credential" json:"credential"`
}

type Config struct {
	Version        int                `yaml:"version" json:"version"`
	CurrentProfile string             `yaml:"currentProfile,omitempty" json:"currentProfile,omitempty"`
	Profiles       map[string]Profile `yaml:"profiles" json:"profiles"`
}

func (c Config) Validate() error {
	if c.Version != CurrentVersion {
		return fmt.Errorf("config version %d is unsupported; want %d", c.Version, CurrentVersion)
	}
	if c.CurrentProfile != "" {
		if _, ok := c.Profiles[c.CurrentProfile]; !ok {
			return fmt.Errorf("current profile %q does not exist", c.CurrentProfile)
		}
	}
	for name, profile := range c.Profiles {
		if strings.TrimSpace(name) == "" {
			return errors.New("profile name must not be empty")
		}
		if _, err := NormalizeServerURL(profile.URL); err != nil {
			return fmt.Errorf("profile %q URL: %w", name, err)
		}
		if err := profile.Credential.Validate(); err != nil {
			return fmt.Errorf("profile %q %w", name, err)
		}
	}
	return nil
}

func NormalizeServerURL(raw string) (string, error) {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil {
		return "", errors.New("must not contain embedded credentials")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("must not contain a query or fragment")
	}
	if parsed.Scheme == "http" {
		host := parsed.Hostname()
		ip := net.ParseIP(host)
		if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
			return "", errors.New("plain HTTP is allowed only for loopback servers")
		}
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

type Store struct {
	Path string
}

func (s Store) Load() (Config, error) {
	info, err := os.Lstat(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{Version: CurrentVersion, Profiles: map[string]Profile{}}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("inspect config: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Config{}, errors.New("config must not be a symbolic link")
	}
	if !info.Mode().IsRegular() {
		return Config{}, errors.New("config must be a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return Config{}, fmt.Errorf("%w: config %s is %#o; run chmod 600 %s", ErrInsecurePermissions, s.Path, info.Mode().Perm(), s.Path)
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (s Store) Save(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	var data bytes.Buffer
	encoder := yaml.NewEncoder(&data)
	encoder.SetIndent(4)
	if err := encoder.Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("close config encoder: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure temporary config: %w", err)
	}
	if _, err := tmp.Write(data.Bytes()); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(tmpPath, s.Path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}
