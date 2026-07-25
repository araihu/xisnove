package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/xisnove/internal/domain"
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

func TestAgentAcceptsEachProbeCapabilityExactlyOnce(t *testing.T) {
	capabilities := []domain.AgentCapability{
		domain.CapabilityHTTP,
		domain.CapabilityTCP,
		domain.CapabilityDNS,
	}
	agent, err := domain.NewAgent(domain.NewAgentParams{
		ID: "agent-1", LocationID: "location-1", Name: "edge",
		Capabilities: capabilities, CredentialGeneration: 1,
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(agent.Capabilities) != 3 {
		t.Fatalf("capabilities = %#v", agent.Capabilities)
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
