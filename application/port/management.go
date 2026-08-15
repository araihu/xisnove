package port

import (
	"context"
	"time"

	"github.com/araihu/xisnove/domain"
)

// MonitorRecord is the presentation-neutral aggregate returned by management
// reads. It keeps the single v1 location assignment beside the Monitor without
// exposing generated transport or SQL types.
type MonitorRecord struct {
	Monitor          domain.Monitor
	LocationID       domain.LocationID
	RequiredLocation bool
}

type StringKeysetRequest struct {
	Limit     int
	AfterSort string
	AfterID   string
	HasAfter  bool
}

type IntKeysetRequest struct {
	Limit     int
	AfterSort int64
	AfterID   string
	HasAfter  bool
}

type TimeKeysetRequest struct {
	Limit     int
	AfterSort time.Time
	AfterID   string
	HasAfter  bool
}

type IncidentResolutionFilter string

const (
	IncidentResolutionAll      IncidentResolutionFilter = ""
	IncidentResolutionOpen     IncidentResolutionFilter = "open"
	IncidentResolutionResolved IncidentResolutionFilter = "resolved"
)

type IncidentListRequest struct {
	TimeKeysetRequest
	Resolution IncidentResolutionFilter
}

type SearchRequest struct {
	Query string
	Limit int
}

type SearchResourceType string

const SearchResourceMonitor SearchResourceType = "monitor"

// SearchResult is a transport-neutral ranked resource projection. Repository
// order is authoritative; callers must not re-rank or broaden this bounded set.
type SearchResult struct {
	ResourceType SearchResourceType
	ResourceID   string
	Title        string
	Description  string
	Context      string
}

type AgentCredentialRecord struct {
	AgentID             domain.AgentID
	Generation          uint64
	CredentialHash      []byte
	CreatedAt           time.Time
	RevokedAt           *time.Time
	LastAuthenticatedAt *time.Time
}

// CreateAgentCredentialGenerationCommand performs the rotation compare-and-set.
// Credential.Generation must equal ExpectedCurrentGeneration+1. Implementations
// return false without mutation when the current generation changed or two
// active generations already exist.
type CreateAgentCredentialGenerationCommand struct {
	ExpectedCurrentGeneration uint64
	Credential                AgentCredentialRecord
}

type CredentialGenerationRevokeOutcome string

const (
	CredentialGenerationRevoked               CredentialGenerationRevokeOutcome = "revoked"
	CredentialGenerationAlreadyRevoked        CredentialGenerationRevokeOutcome = "already-revoked"
	CredentialGenerationNotFound              CredentialGenerationRevokeOutcome = "not-found"
	CredentialGenerationCurrent               CredentialGenerationRevokeOutcome = "current"
	CredentialGenerationReplacementUnobserved CredentialGenerationRevokeOutcome = "replacement-unobserved"
)

// ManagementCommandRepository contains only atomic relational mutations used
// by management application services. Each method runs inside the caller's
// UnitOfWork transaction; booleans are compare-and-set outcomes, not errors.
type ManagementCommandRepository interface {
	ReplaceLocation(context.Context, domain.Location) (bool, error)
	DisableLocation(context.Context, domain.LocationID, time.Time) (bool, error)
	ReplaceMonitor(context.Context, MonitorRecord) (bool, error)
	DisableMonitor(context.Context, domain.MonitorID, time.Time) (bool, error)
	UpdateAgent(context.Context, domain.Agent) (bool, error)
	RevokeAgent(context.Context, domain.AgentID, time.Time) (bool, error)
	CreateAgentCredentialGeneration(context.Context, CreateAgentCredentialGenerationCommand) (bool, error)
	GetAgentCredentialGeneration(context.Context, domain.AgentID, uint64) (AgentCredentialRecord, error)
	RevokeAgentCredentialGeneration(context.Context, domain.AgentID, uint64, time.Time) (CredentialGenerationRevokeOutcome, error)
}

// ManagementQueryRepository is the storage-facing read model for public
// management APIs. Implementations fetch at most request.Limit rows; the
// application requests page-size+1 and alone constructs the public page and
// opaque continuation cursor.
type ManagementQueryRepository interface {
	SearchResources(context.Context, SearchRequest) ([]SearchResult, error)
	GetLocation(context.Context, domain.LocationID) (domain.Location, error)
	ListLocations(context.Context, StringKeysetRequest) ([]domain.Location, error)
	GetMonitor(context.Context, domain.MonitorID) (MonitorRecord, error)
	ListMonitors(context.Context, IntKeysetRequest) ([]MonitorRecord, error)
	GetAgent(context.Context, domain.AgentID) (domain.Agent, error)
	ListAgents(context.Context, StringKeysetRequest) ([]domain.Agent, error)
	GetIncident(context.Context, domain.IncidentID) (domain.Incident, error)
	ListIncidents(context.Context, IncidentListRequest) ([]domain.Incident, error)
	ListIncidentEvents(context.Context, domain.IncidentID, TimeKeysetRequest) ([]domain.IncidentEvent, error)
}
