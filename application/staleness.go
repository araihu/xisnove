package application

import (
	"context"
	"fmt"
	"time"

	"github.com/araihu/xisnove/domain"
)

type StalenessService struct {
	store                    UnitOfWork
	newID                    func() string
	observeMonitorTransition func(MonitorTransitionObservation)
}

func NewStalenessService(store UnitOfWork, newID func() string) *StalenessService {
	return NewStalenessServiceWithObserver(store, newID, nil)

}

// NewStalenessServiceWithObserver preserves the original constructor while
// allowing the composition root to observe committed aggregate transitions.
func NewStalenessServiceWithObserver(
	store UnitOfWork,
	newID func() string,
	observe func(MonitorTransitionObservation),
) *StalenessService {
	return &StalenessService{store: store, newID: newID, observeMonitorTransition: observe}
}

func (s *StalenessService) MarkDue(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	var now time.Time
	var due []domain.LocationHealth
	err := s.store.View(ctx, func(ctx context.Context, repositories Repositories) error {
		var err error
		now, err = repositories.Runs.DatabaseNow(ctx)
		if err != nil {
			return fmt.Errorf("read database time: %w", err)
		}
		due, err = repositories.Health.ListStale(ctx, now, limit)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("list stale locations: %w", err)
	}
	marked := 0
	for _, candidate := range due {
		changed := false
		transitioned := false
		var transition MonitorTransitionObservation
		err := s.store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
			at, err := repositories.Runs.DatabaseNow(ctx)
			if err != nil {
				return fmt.Errorf("read claim database time: %w", err)
			}
			eligible, err := staleCandidateEligible(ctx, repositories, candidate, at)
			if err != nil || !eligible {
				return err
			}
			changed, err = repositories.Health.ClaimStale(
				ctx,
				candidate.MonitorID,
				candidate.LocationID,
				candidate.StaleAt,
				at,
			)
			if err != nil || !changed {
				return err
			}
			transition, transitioned, err = projectAggregateAndIncidentObserved(
				ctx,
				repositories,
				candidate.MonitorID,
				at,
				s.newID,
				true,
			)
			if err != nil {
				return err
			}
			return appendStaleStateTick(
				ctx, repositories, candidate, transition.To, at, s.newID,
			)
		})
		if err != nil {
			return marked, fmt.Errorf(
				"mark location %s/%s stale: %w",
				domain.MonitorID(candidate.MonitorID),
				domain.LocationID(candidate.LocationID),
				err,
			)
		}
		if changed {
			marked++
		}
		if transitioned && s.observeMonitorTransition != nil {
			s.observeMonitorTransition(transition)
		}
	}
	return marked, nil
}

// staleCandidateEligible revalidates administrative state inside the claim
// transaction. The initial ListStale read may race a disable, location pause,
// or maintenance start; none of those candidates may be claimed or projected
// as an unknown observation.
func staleCandidateEligible(
	ctx context.Context,
	repositories Repositories,
	candidate domain.LocationHealth,
	now time.Time,
) (bool, error) {
	monitor, err := repositories.Monitors.Get(ctx, candidate.MonitorID)
	if err != nil {
		return false, err
	}
	if !monitor.Enabled {
		return false, nil
	}
	location, err := repositories.Locations.Get(ctx, candidate.LocationID)
	if err != nil {
		return false, err
	}
	if !location.Enabled {
		return false, nil
	}
	activeMaintenance, err := repositories.Maintenance.ListActive(ctx, candidate.MonitorID, now)
	if err != nil {
		return false, err
	}
	return len(activeMaintenance) == 0, nil
}
