package domain

import (
	"errors"
	"strings"
	"time"
)

var ErrInvalidAgent = errors.New("invalid agent")

type AgentCapability string

const (
	CapabilityHTTP AgentCapability = "http"
	CapabilityTCP  AgentCapability = "tcp"
	CapabilityDNS  AgentCapability = "dns"
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
			capability != CapabilityDNS {
			return ErrInvalidAgent
		}
		if _, exists := seen[capability]; exists {
			return ErrInvalidAgent
		}
		seen[capability] = struct{}{}
	}
	return nil
}
