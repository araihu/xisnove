package application

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

const (
	defaultPublicStatusHistoryDays = 30
	maxPublicStatusHistoryDays     = 90
)

type PublicIncidentSummary struct {
	ID               domain.IncidentID
	MonitorID        domain.MonitorID
	MonitorName      string
	State            domain.HealthState
	Severity         domain.IncidentSeverity
	OpenedAt         time.Time
	LastTransitionAt time.Time
}

type PublicStatusMonitor struct {
	ID               domain.MonitorID
	Name             string
	Description      string
	DisplayOrder     int32
	State            domain.HealthState
	LastTransitionAt time.Time
	ActiveIncident   *PublicIncidentSummary
	Uptime           []port.DailyUptimeRecord
}

type PublicStatusPage struct {
	State           domain.HealthState
	GeneratedAt     time.Time
	Monitors        []PublicStatusMonitor
	ActiveIncidents []PublicIncidentSummary
}

type PublicStatusServiceConfig struct {
	Store       port.PublicStatusUnitOfWork
	HistoryDays int
	Now         func() time.Time
}

type PublicStatusService struct {
	store       port.PublicStatusUnitOfWork
	historyDays int
	now         func() time.Time
}

func NewPublicStatusService(config PublicStatusServiceConfig) (*PublicStatusService, error) {
	if config.Store == nil {
		return nil, fmt.Errorf("public status store is required")
	}
	days := config.HistoryDays
	if days == 0 {
		days = defaultPublicStatusHistoryDays
	}
	if days < 1 || days > maxPublicStatusHistoryDays {
		return nil, fmt.Errorf("public status history days must be between 1 and %d", maxPublicStatusHistoryDays)
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &PublicStatusService{store: config.Store, historyDays: days, now: now}, nil
}

func (s *PublicStatusService) Get(ctx context.Context) (PublicStatusPage, error) {
	generatedAt := s.now().UTC()
	today := time.Date(generatedAt.Year(), generatedAt.Month(), generatedAt.Day(), 0, 0, 0, 0, time.UTC)
	start := today.AddDate(0, 0, -s.historyDays)

	page := PublicStatusPage{
		State: domain.HealthUp, GeneratedAt: generatedAt,
		Monitors: make([]PublicStatusMonitor, 0), ActiveIncidents: make([]PublicIncidentSummary, 0),
	}
	err := s.store.View(ctx, func(ctx context.Context, repositories port.PublicStatusRepositories) error {
		rows, err := repositories.Status.ListMonitors(ctx)
		if err != nil {
			return err
		}
		page.Monitors = make([]PublicStatusMonitor, 0, len(rows))
		page.ActiveIncidents = make([]PublicIncidentSummary, 0, len(rows))
		for _, row := range rows {
			uptime, err := repositories.Retention.ListDailyUptime(ctx, row.ID, start, today)
			if err != nil {
				return err
			}
			monitor := PublicStatusMonitor{
				ID: row.ID, Name: row.Name, Description: row.Description,
				DisplayOrder: row.DisplayOrder, State: row.State,
				LastTransitionAt: row.LastTransitionAt,
				Uptime:           slices.Clone(uptime),
			}
			if row.ActiveIncident != nil {
				incident := PublicIncidentSummary{
					ID: row.ActiveIncident.ID, MonitorID: row.ID, MonitorName: row.Name,
					State: row.ActiveIncident.State, Severity: row.ActiveIncident.Severity,
					OpenedAt:         row.ActiveIncident.OpenedAt,
					LastTransitionAt: row.ActiveIncident.LastTransitionAt,
				}
				monitor.ActiveIncident = &incident
				page.ActiveIncidents = append(page.ActiveIncidents, incident)
			}
			page.Monitors = append(page.Monitors, monitor)
			page.State = higherPublicStatusState(page.State, row.State)
		}
		return nil
	})
	if err != nil {
		return PublicStatusPage{}, fmt.Errorf("get public status: %w", err)
	}
	return page, nil
}

func higherPublicStatusState(current, candidate domain.HealthState) domain.HealthState {
	if publicStatusRank(candidate) > publicStatusRank(current) {
		return candidate
	}
	return current
}

func publicStatusRank(state domain.HealthState) int {
	switch state {
	case domain.HealthDown:
		return 5
	case domain.HealthDegraded:
		return 4
	case domain.HealthUnknown:
		return 3
	case domain.HealthPending:
		return 2
	case domain.HealthUp:
		return 1
	default:
		return 3
	}
}
