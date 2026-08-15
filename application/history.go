package application

import (
	"context"
	"fmt"
	"time"

	"github.com/araihu/xisnove/domain"
)

const (
	DefaultMonitorHistoryWindow = 3 * time.Hour
	DefaultMonitorHistoryLimit  = 4096
	MaxMonitorHistoryLimit      = 10000
	maxMonitorHistoryQueryLimit = MaxMonitorHistoryLimit + 1
)

// MonitorHistoryView is a bounded, redacted view for operational charts.
// Samples are accepted probe results ordered by observation time.
type MonitorHistoryView struct {
	MonitorID   domain.MonitorID
	StartsAt    time.Time
	EndsAt      time.Time
	GeneratedAt time.Time
	Samples     []ProbeHistoryRecord
	Truncated   bool
}

type MonitorHistoryService struct {
	store UnitOfWork
	now   func() time.Time
}

func NewMonitorHistoryService(store UnitOfWork) *MonitorHistoryService {
	return NewMonitorHistoryServiceWithClock(store, func() time.Time { return time.Now().UTC() })
}

func NewMonitorHistoryServiceWithClock(store UnitOfWork, now func() time.Time) *MonitorHistoryService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &MonitorHistoryService{store: store, now: now}
}

// GetMonitorAvailabilityHistory returns accepted probe samples in [start,end).
// Missing samples are intentionally not fabricated here; renderers may map
// gaps to Unknown without confusing absence with a failed probe.
func (s *MonitorHistoryService) GetMonitorAvailabilityHistory(
	ctx context.Context,
	monitorID domain.MonitorID,
	startsAt *time.Time,
	endsAt *time.Time,
	limit *int,
) (MonitorHistoryView, error) {
	now := s.now().UTC()
	end := now
	if endsAt != nil {
		end = endsAt.UTC()
	}
	start := end.Add(-DefaultMonitorHistoryWindow)
	if startsAt != nil {
		start = startsAt.UTC()
	}
	if end.After(now) {
		return MonitorHistoryView{}, &ValidationError{Fields: map[string]string{"endsAt": "must not be in the future"}}
	}
	if !end.After(start) {
		return MonitorHistoryView{}, &ValidationError{Fields: map[string]string{"window": "endsAt must be after startsAt"}}
	}
	if end.Sub(start) > DefaultMonitorHistoryWindow {
		return MonitorHistoryView{}, &ValidationError{Fields: map[string]string{"window": "must not exceed three hours"}}
	}
	requestedLimit := DefaultMonitorHistoryLimit
	if limit != nil {
		requestedLimit = *limit
	}
	if requestedLimit < 1 || requestedLimit > MaxMonitorHistoryLimit {
		return MonitorHistoryView{}, &ValidationError{Fields: map[string]string{"limit": fmt.Sprintf("must be between 1 and %d", MaxMonitorHistoryLimit)}}
	}

	var samples []ProbeHistoryRecord
	err := s.store.View(ctx, func(ctx context.Context, repositories Repositories) error {
		if _, err := repositories.Monitors.Get(ctx, monitorID); err != nil {
			return err
		}
		// Fetch one extra newest sample so the response can report truncation.
		var err error
		samples, err = repositories.Results.ListMonitorHistory(ctx, monitorID, start, end, requestedLimit+1)
		return err
	})
	if err != nil {
		return MonitorHistoryView{}, err
	}
	truncated := len(samples) > requestedLimit
	if truncated {
		samples = samples[len(samples)-requestedLimit:]
	}
	return MonitorHistoryView{
		MonitorID: monitorID, StartsAt: start, EndsAt: end, GeneratedAt: now,
		Samples: samples, Truncated: truncated,
	}, nil
}

// NormalizeMonitorHistoryQueryLimit keeps one extra row available for the
// service's truncation check while preserving the public 10,000-row limit.
func NormalizeMonitorHistoryQueryLimit(limit int) int {
	if limit <= 0 {
		return DefaultMonitorHistoryLimit
	}
	if limit > maxMonitorHistoryQueryLimit {
		return maxMonitorHistoryQueryLimit
	}
	return limit
}
