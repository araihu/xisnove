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

type SchedulerStats struct {
	Inserted         int
	SkippedIntervals uint64
	MaximumLag       time.Duration
}

func NewScheduler(store Store, newID func() string) *Scheduler {
	return &Scheduler{store: store, newID: newID}
}

func (s *Scheduler) EnqueueDue(ctx context.Context, limit int) (int, error) {
	stats, err := s.EnqueueDueWithStats(ctx, limit)
	return stats.Inserted, err
}

func (s *Scheduler) EnqueueDueWithStats(
	ctx context.Context,
	limit int,
) (SchedulerStats, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	now, err := s.store.Repositories().Runs.DatabaseNow(ctx)
	if err != nil {
		return SchedulerStats{}, fmt.Errorf("read database time: %w", err)
	}
	due, err := s.store.Repositories().Monitors.ListDue(ctx, now, limit)
	if err != nil {
		return SchedulerStats{}, fmt.Errorf("list due monitors: %w", err)
	}

	stats := SchedulerStats{}
	for _, item := range due {
		scheduledFor, nextRunAt, skipped, err := boundedSchedule(
			item.NextRunAt,
			item.Monitor.Interval,
			now,
		)
		if err != nil {
			return stats, err
		}
		lag := now.Sub(item.NextRunAt)
		if lag > stats.MaximumLag {
			stats.MaximumLag = lag
		}
		run := NewRunRecord{
			ID:           domain.CheckRunID(s.newID()),
			MonitorID:    item.Monitor.ID,
			LocationID:   item.LocationID,
			ScheduledFor: scheduledFor,
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
			return stats, err
		}
		if created {
			stats.Inserted++
			stats.SkippedIntervals += skipped
		}
	}
	return stats, nil
}

func boundedSchedule(
	scheduledAt time.Time,
	interval time.Duration,
	now time.Time,
) (time.Time, time.Time, uint64, error) {
	if scheduledAt.IsZero() || interval <= 0 {
		return time.Time{}, time.Time{}, 0, errors.New("invalid monitor schedule")
	}
	scheduledAt = scheduledAt.UTC()
	now = now.UTC()
	if scheduledAt.After(now) {
		return scheduledAt, scheduledAt, 0, nil
	}
	skipped := now.Sub(scheduledAt) / interval
	latest := scheduledAt.Add(skipped * interval)
	return latest, latest.Add(interval), uint64(skipped), nil
}
