package main

import (
	"net/netip"
	"testing"
)

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
