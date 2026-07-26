package domain

import (
	"errors"
	"strings"
	"time"
)

var ErrInvalidAgent = errors.New("invalid agent")

type AgentCapability string

const (
	CapabilityHTTP                AgentCapability = "http"
	CapabilityTCP                 AgentCapability = "tcp"
	CapabilityDNS                 AgentCapability = "dns"
	CapabilityKubernetesDiscovery AgentCapability = "kubernetes-discovery"
	CapabilityKubernetesWatch     AgentCapability = "kubernetes-watch"
)

type Agent struct {
	ID                   AgentID
	LocationID           LocationID
	Name                 string
	Capabilities         []AgentCapability
	CredentialGeneration uint64
	Version              string
	LastSeenAt           time.Time
	RevokedAt            *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type NewAgentParams struct {
	ID                   AgentID
	LocationID           LocationID
	Name                 string
	Capabilities         []AgentCapability
	CredentialGeneration uint64
	CreatedAt            time.Time
}

func NewAgent(params NewAgentParams) (Agent, error) {
	name := strings.TrimSpace(params.Name)
	if params.ID == "" ||
		params.LocationID == "" ||
		name == "" ||
		params.CredentialGeneration == 0 ||
		ValidateAgentCapabilities(params.Capabilities) != nil {
		return Agent{}, ErrInvalidAgent
	}
	return Agent{
		ID:                   params.ID,
		LocationID:           params.LocationID,
		Name:                 name,
		Capabilities:         append([]AgentCapability(nil), params.Capabilities...),
		CredentialGeneration: params.CredentialGeneration,
		CreatedAt:            params.CreatedAt.UTC(),
		UpdatedAt:            params.CreatedAt.UTC(),
	}, nil
}

func ValidateAgentCapabilities(capabilities []AgentCapability) error {
	if len(capabilities) == 0 {
		return ErrInvalidAgent
	}
	seen := make(map[AgentCapability]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if capability != CapabilityHTTP &&
			capability != CapabilityTCP &&
			capability != CapabilityDNS &&
			capability != CapabilityKubernetesDiscovery &&
			capability != CapabilityKubernetesWatch {
			return ErrInvalidAgent
		}
		if _, exists := seen[capability]; exists {
			return ErrInvalidAgent
		}
		seen[capability] = struct{}{}
	}
	return nil
}

// ProbeCapabilities returns only capabilities that can claim monitor work.
// Discovery capabilities describe a separate publication loop and must never
// be treated as probe kinds by the scheduler or lease service.
func ProbeCapabilities(capabilities []AgentCapability) []AgentCapability {
	probes := make([]AgentCapability, 0, len(capabilities))
	for _, capability := range capabilities {
		switch capability {
		case CapabilityHTTP, CapabilityTCP, CapabilityDNS:
			probes = append(probes, capability)
		}
	}
	return probes
}
