package main

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/araihu/xisnove/agent/internal/controlplane"
)

func TestCredentialProviderReadsOnlyConfiguredBundleFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.json")
	if err := os.WriteFile(path, []byte(`{"credential":"credential-from-file","generation":17}`), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, err := credentialProvider(config{credentialFile: path}).Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Credential != "credential-from-file" || bundle.Generation != 17 {
		t.Fatalf("bundle = %#v", bundle)
	}
}

func TestLoadConfig(t *testing.T) {
	values := map[string]string{
		"XISNOVE_URL":                         "https://monitor.example.com/",
		"XISNOVE_AGENT_CREDENTIAL_FILE":       "/run/secrets/agent",
		"XISNOVE_AGENT_ALLOWED_PRIVATE_CIDRS": "10.0.0.0/8, 192.168.0.0/16",
	}

	config, err := loadConfig(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if config.controlPlaneURL != "https://monitor.example.com" {
		t.Fatalf("control plane URL = %q", config.controlPlaneURL)
	}
	if config.credentialFile != "/run/secrets/agent" {
		t.Fatalf("credential file = %q", config.credentialFile)
	}
	wantCapabilities := []controlplane.AgentCapability{
		controlplane.AgentCapabilityHttp,
		controlplane.AgentCapabilityTcp,
		controlplane.AgentCapabilityDns,
	}
	if len(config.capabilities) != len(wantCapabilities) {
		t.Fatalf("capabilities = %#v", config.capabilities)
	}
	for index := range wantCapabilities {
		if config.capabilities[index] != wantCapabilities[index] {
			t.Fatalf("capabilities[%d] = %s", index, config.capabilities[index])
		}
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("192.168.0.0/16"),
	}
	if len(config.allowedPrivate) != len(want) {
		t.Fatalf("allowed private = %#v", config.allowedPrivate)
	}
	for index := range want {
		if config.allowedPrivate[index] != want[index] {
			t.Fatalf("allowed private[%d] = %s", index, config.allowedPrivate[index])
		}
	}
}

func TestLoadConfigRejectsMissingOrUnsafeValues(t *testing.T) {
	tests := map[string]map[string]string{
		"missing URL": {
			"XISNOVE_AGENT_CREDENTIAL_FILE": "/run/secrets/agent",
		},
		"unsupported URL scheme": {
			"XISNOVE_URL":                   "file:///tmp/control-plane",
			"XISNOVE_AGENT_CREDENTIAL_FILE": "/run/secrets/agent",
		},
		"missing credential file": {
			"XISNOVE_URL": "https://monitor.example.com",
		},
		"invalid CIDR": {
			"XISNOVE_URL":                         "https://monitor.example.com",
			"XISNOVE_AGENT_CREDENTIAL_FILE":       "/run/secrets/agent",
			"XISNOVE_AGENT_ALLOWED_PRIVATE_CIDRS": "not-a-cidr",
		},
		"invalid capability": {
			"XISNOVE_URL":                   "https://monitor.example.com",
			"XISNOVE_AGENT_CREDENTIAL_FILE": "/run/secrets/agent",
			"XISNOVE_AGENT_CAPABILITIES":    "http,exec",
		},
	}

	for name, values := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := loadConfig(func(key string) string { return values[key] })
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
