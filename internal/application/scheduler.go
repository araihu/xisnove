package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/araihu/xisnove/internal/domain"
)

type Scheduler struct {
	store Store
	newID func() string
}

func NewScheduler(store Store, newID func() string) *Scheduler {
	return &Scheduler{store: store, newID: newID}
}

func (s *Scheduler) EnqueueDue(ctx context.Context, limit int) (int, error) {
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
	due, err := s.store.Repositories().Monitors.ListDue(ctx, now, limit)
	if err != nil {
		return 0, fmt.Errorf("list due monitors: %w", err)
	}

	inserted := 0
	for _, item := range due {
		nextRunAt, err := nextSchedule(item.NextRunAt, item.Monitor.Interval, now)
		if err != nil {
			return inserted, err
		}
		run := NewRunRecord{
			ID:           domain.CheckRunID(s.newID()),
			MonitorID:    item.Monitor.ID,
			LocationID:   item.LocationID,
			ScheduledFor: item.NextRunAt,
			Probe:        item.Monitor.Probe(),
			Timeout:      item.Monitor.Timeout,
		}
		created := false
		err = s.store.WithinTx(ctx, func(repositories Repositories) error {
			created, err = repositories.Runs.Insert(ctx, run)
			if err != nil {
				return fmt.Errorf("insert scheduled run: %w", err)
			}
			if _, err := repositories.Monitors.AdvanceNextRun(
				ctx,
				item.Monitor.ID,
				nextRunAt,
				now,
			); err != nil {
				return fmt.Errorf("advance monitor schedule: %w", err)
			}
			return nil
		})
		if err != nil {
			return inserted, err
		}
		if created {
			inserted++
		}
	}
	return inserted, nil
}

func nextSchedule(scheduledAt time.Time, interval time.Duration, now time.Time) (time.Time, error) {
	if scheduledAt.IsZero() || interval <= 0 {
		return time.Time{}, errors.New("invalid monitor schedule")
	}
	scheduledAt = scheduledAt.UTC()
	now = now.UTC()
	if scheduledAt.After(now) {
		return scheduledAt, nil
	}
	steps := now.Sub(scheduledAt)/interval + 1
	return scheduledAt.Add(steps * interval), nil
}
