package port

import (
	"context"
	"time"

	"github.com/araihu/xisnove/domain"
)

// PublicMonitorProjection is the deliberately narrow storage projection used
// by the anonymous status page. It contains no probe, location, agent, result,
// notification, audit, or secret data.
type PublicMonitorProjection struct {
	ID               domain.MonitorID
	Name             string
	Description      string
	DisplayOrder     int32
	State            domain.HealthState
	LastTransitionAt time.Time
	ActiveIncident   *domain.Incident
}

type PublicStatusRepository interface {
	ListMonitors(context.Context) ([]PublicMonitorProjection, error)
}

type PublicStatusRepositories struct {
	Status    PublicStatusRepository
	Retention RetentionRepository
}

// PublicStatusUnitOfWork provides one consistent read snapshot across the
// status projection and its bounded daily uptime records.
type PublicStatusUnitOfWork interface {
	View(context.Context, func(context.Context, PublicStatusRepositories) error) error
}
