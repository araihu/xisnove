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
			changed, err = repositories.Health.ClaimStale(
				ctx,
				candidate.MonitorID,
				candidate.LocationID,
				candidate.StaleAt,
				now,
			)
			if err != nil || !changed {
				return err
			}
			transition, transitioned, err = projectAggregateAndIncidentObserved(
				ctx,
				repositories,
				candidate.MonitorID,
				now,
				s.newID,
				true,
			)
			return err
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
