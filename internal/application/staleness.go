package application

import (
	"context"
	"fmt"

	"github.com/araihu/xisnove/internal/domain"
)

type StalenessService struct {
	store Store
	newID func() string
}

func NewStalenessService(store Store, newID func() string) *StalenessService {
	return &StalenessService{store: store, newID: newID}
}

func (s *StalenessService) MarkDue(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	now, err := s.store.Repositories().Runs.DatabaseNow(ctx)
	if err != nil {
		return 0, fmt.Errorf("read database time: %w", err)
	}
	due, err := s.store.Repositories().Health.ListStale(ctx, now, limit)
	if err != nil {
		return 0, fmt.Errorf("list stale locations: %w", err)
	}
	marked := 0
	for _, candidate := range due {
		changed := false
		err := s.store.WithinTx(ctx, func(repositories Repositories) error {
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
			return projectAggregateAndIncident(
				ctx,
				repositories,
				candidate.MonitorID,
				now,
				s.newID,
				true,
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
	}
	return marked, nil
}
