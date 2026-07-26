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
	DiscoveryStateStale    DiscoveryState = "stale"
)

type DiscoveryFilter struct {
	State DiscoveryState
}

type DiscoveryListRequest struct {
	Filter DiscoveryFilter
	Limit  int
	After  domain.DiscoveryCandidateID
}

type DiscoveryRepository interface {
	ApplyBatch(context.Context, DiscoveryBatch) (DiscoveryBatchAcknowledgement, error)
	Get(context.Context, domain.DiscoveryCandidateID) (domain.DiscoveryCandidate, error)
	List(context.Context, DiscoveryListRequest) ([]domain.DiscoveryCandidate, error)
	LinkPromotion(context.Context, domain.DiscoveryCandidateID, domain.MonitorID, time.Time) (bool, error)
}

type DiscoveryRepositories struct {
	Discovery DiscoveryRepository
	Locations LocationRepository
	Monitors  MonitorRepository
	Health    HealthRepository
}

type DiscoveryUnitOfWork interface {
	DiscoveryView(context.Context, func(context.Context, DiscoveryRepositories) error) error
	DiscoveryTransact(context.Context, func(context.Context, DiscoveryRepositories) error) error
}
