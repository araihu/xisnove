package port

import (
	"context"
	"time"

	"github.com/araihu/xisnove/domain"
)

type DiscoveryBatch struct {
	ID          string
	AgentID     domain.AgentID
	RequestHash string
	Candidates  []domain.DiscoveryCandidate
	// Complete identifies a full point-in-time snapshot. CompletedAt is the
	// client observation timestamp; CreatedAt remains the server commit time.
	Complete    bool
	CompletedAt time.Time
	CreatedAt   time.Time
}

type DiscoveryBatchAcknowledgement struct {
	Accepted int
	Created  int
	Updated  int
}

type DiscoveryState string

const (
	DiscoveryStateAll      DiscoveryState = ""
	DiscoveryStatePending  DiscoveryState = "pending"
	DiscoveryStatePromoted DiscoveryState = "promoted"
)

type DiscoveryFilter struct {
	State   DiscoveryState
	Present *bool
}

type DiscoveryListRequest struct {
	Filter DiscoveryFilter
	Limit  int
	After  domain.DiscoveryCandidateID
}

type DiscoveryRepository interface {
	ApplyBatch(context.Context, DiscoveryBatch) (DiscoveryBatchAcknowledgement, error)
	Get(context.Context, domain.DiscoveryCandidateID) (domain.DiscoveryCandidate, error)
	GetForUpdate(context.Context, domain.DiscoveryCandidateID) (domain.DiscoveryCandidate, error)
	List(context.Context, DiscoveryListRequest) ([]domain.DiscoveryCandidate, error)
	LinkPromotion(context.Context, domain.DiscoveryCandidateID, domain.MonitorID, time.Time) (bool, error)
}

// CompleteDiscoveryRepository is an optional observation read model retained
// separately so existing delta-only publishers need not implement it.
type CompleteDiscoveryRepository interface {
	LastCompleteAt(context.Context, domain.AgentID) (*time.Time, error)
}

type DiscoveryRepositories struct {
	Discovery       DiscoveryRepository
	Agents          AgentRepository
	Locations       LocationRepository
	Monitors        MonitorRepository
	Health          HealthRepository
	StateTickWriter StateTickWriter
}

type DiscoveryUnitOfWork interface {
	DiscoveryView(context.Context, func(context.Context, DiscoveryRepositories) error) error
	DiscoveryTransact(context.Context, func(context.Context, DiscoveryRepositories) error) error
}
