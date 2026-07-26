package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/xisnove/domain"
)

func TestNewAgentNormalizesIdentityAndCapabilities(t *testing.T) {
	now := time.Date(2026, 7, 25, 1, 2, 3, 0, time.FixedZone("test", -3*60*60))
	agent, err := domain.NewAgent(domain.NewAgentParams{
		ID:                   "agent-1",
		LocationID:           "location-1",
		Name:                 " vps-1 ",
		Capabilities:         []domain.AgentCapability{domain.CapabilityHTTP},
		CredentialGeneration: 1,
		CreatedAt:            now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if agent.Name != "vps-1" {
		t.Fatalf("Name = %q", agent.Name)
	}
	if agent.CreatedAt.Location() != time.UTC || !agent.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt = %v", agent.CreatedAt)
	}
}

func TestNewAgentRejectsMissingCapability(t *testing.T) {
	_, err := domain.NewAgent(domain.NewAgentParams{
		ID:                   "agent-1",
		LocationID:           "location-1",
		Name:                 "vps-1",
		CredentialGeneration: 1,
		CreatedAt:            time.Now(),
	})
	if !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("error = %v", err)
	}
}

func TestNewAgentRejectsUnknownCapability(t *testing.T) {
	_, err := domain.NewAgent(domain.NewAgentParams{
		ID:                   "agent-1",
		LocationID:           "location-1",
		Name:                 "vps-1",
		Capabilities:         []domain.AgentCapability{"root-shell"},
		CredentialGeneration: 1,
		CreatedAt:            time.Now(),
	})
	if !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("error = %v", err)
	}
}

func TestAgentAcceptsEachCapabilityExactlyOnce(t *testing.T) {
	capabilities := []domain.AgentCapability{
		domain.CapabilityHTTP,
		domain.CapabilityTCP,
		domain.CapabilityDNS,
		domain.CapabilityKubernetesDiscovery,
		domain.CapabilityKubernetesWatch,
	}
	agent, err := domain.NewAgent(domain.NewAgentParams{
		ID: "agent-1", LocationID: "location-1", Name: "edge",
		Capabilities: capabilities, CredentialGeneration: 1,
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(agent.Capabilities) != 5 {
		t.Fatalf("capabilities = %#v", agent.Capabilities)
	}
}

func TestProbeCapabilitiesExcludeDiscoveryWork(t *testing.T) {
	got := domain.ProbeCapabilities([]domain.AgentCapability{
		domain.CapabilityKubernetesDiscovery,
		domain.CapabilityHTTP,
		domain.CapabilityKubernetesWatch,
		domain.CapabilityDNS,
	})
	want := []domain.AgentCapability{domain.CapabilityHTTP, domain.CapabilityDNS}
	if len(got) != len(want) {
		t.Fatalf("probe capabilities = %#v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("probe capabilities = %#v", got)
		}
	}
}

func TestAgentRejectsDuplicateProbeCapability(t *testing.T) {
	_, err := domain.NewAgent(domain.NewAgentParams{
		ID: "agent-1", LocationID: "location-1", Name: "edge",
		Capabilities: []domain.AgentCapability{
			domain.CapabilityTCP,
			domain.CapabilityTCP,
		},
		CredentialGeneration: 1,
		CreatedAt:            time.Now(),
	})
	if !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("error = %v", err)
	}
}
