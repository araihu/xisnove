package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/araihu/xisnove/domain"
)

const (
	DefaultStateTickHistoryWindow = 3 * time.Hour
	DefaultStateTickHistoryLimit  = 4096
	MaxStateTickHistoryLimit      = 10000
	maxStateTickHistoryQueryLimit = MaxStateTickHistoryLimit + 1
)

// ErrInvalidStateTickHistory indicates a malformed history query or a
// repository result that violates the immutable history contract.
var ErrInvalidStateTickHistory = errors.New("invalid state tick history")

// StateTickHistoryView is a bounded immutable history snapshot for one
// monitor. Ticks are ordered by occurredAt, then causal parent/child relation,
// then stable tick ID.
type StateTickHistoryView struct {
	MonitorID   domain.MonitorID
	StartsAt    time.Time
	EndsAt      time.Time
	GeneratedAt time.Time
	Ticks       []domain.StateTick
	Truncated   bool
}

// StateTickHistoryService owns query-window validation and public projection
// of immutable state ticks. It does not infer or rewrite probe outcomes.
type StateTickHistoryService struct {
	store UnitOfWork
	now   func() time.Time
}

func NewStateTickHistoryService(store UnitOfWork) *StateTickHistoryService {
	return NewStateTickHistoryServiceWithClock(store, func() time.Time { return time.Now().UTC() })
}

func NewStateTickHistoryServiceWithClock(store UnitOfWork, now func() time.Time) *StateTickHistoryService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &StateTickHistoryService{store: store, now: now}
}

// GetMonitorStateHistory returns immutable state ticks in the UTC half-open
// interval [startsAt,endsAt). Defaults are the last three hours and 4096
// records. Newest records are retained when the requested limit truncates the
// result.
func (s *StateTickHistoryService) GetMonitorStateHistory(
	ctx context.Context,
	monitorID domain.MonitorID,
	startsAt *time.Time,
	endsAt *time.Time,
	limit *int,
) (StateTickHistoryView, error) {
	if strings.TrimSpace(string(monitorID)) == "" {
		return StateTickHistoryView{}, &ValidationError{Fields: map[string]string{"monitorId": "is required"}}
	}
	now := s.now().UTC()
	end := now
	if endsAt != nil {
		end = endsAt.UTC()
	}
	start := end.Add(-DefaultStateTickHistoryWindow)
	if startsAt != nil {
		start = startsAt.UTC()
	}
	if end.After(now) {
		return StateTickHistoryView{}, &ValidationError{Fields: map[string]string{"endsAt": "must not be in the future"}}
	}
	if !end.After(start) {
		return StateTickHistoryView{}, &ValidationError{Fields: map[string]string{"window": "endsAt must be after startsAt"}}
	}
	if end.Sub(start) > DefaultStateTickHistoryWindow {
		return StateTickHistoryView{}, &ValidationError{Fields: map[string]string{"window": "must not exceed three hours"}}
	}
	requestedLimit := DefaultStateTickHistoryLimit
	if limit != nil {
		requestedLimit = *limit
	}
	if requestedLimit < 1 || requestedLimit > MaxStateTickHistoryLimit {
		return StateTickHistoryView{}, &ValidationError{Fields: map[string]string{
			"limit": fmt.Sprintf("must be between 1 and %d", MaxStateTickHistoryLimit),
		}}
	}

	var ticks []domain.StateTick
	err := s.store.View(ctx, func(ctx context.Context, repositories Repositories) error {
		if repositories.Monitors == nil {
			return ErrInvalidStateTickHistory
		}
		if _, err := repositories.Monitors.Get(ctx, monitorID); err != nil {
			return err
		}
		if repositories.StateTicks == nil {
			return ErrInvalidStateTickHistory
		}
		var err error
		ticks, err = repositories.StateTicks.ListStateTicks(
			ctx, monitorID, start, end, requestedLimit+1,
		)
		return err
	})
	if err != nil {
		return StateTickHistoryView{}, err
	}

	projected := make([]domain.StateTick, len(ticks))
	seenIDs := make(map[string]struct{}, len(ticks))
	for index, tick := range ticks {
		if err := tick.Validate(); err != nil || tick.MonitorID != monitorID ||
			tick.OccurredAt.Before(start) || !tick.OccurredAt.Before(end) {
			return StateTickHistoryView{}, ErrInvalidStateTickHistory
		}
		if _, exists := seenIDs[tick.ID]; exists {
			return StateTickHistoryView{}, ErrInvalidStateTickHistory
		}
		seenIDs[tick.ID] = struct{}{}
		projected[index] = tick.Clone()
	}
	slices.SortStableFunc(projected, compareStateTicks)
	truncated := len(projected) > requestedLimit
	if truncated {
		projected = projected[len(projected)-requestedLimit:]
	}
	return StateTickHistoryView{
		MonitorID: monitorID, StartsAt: start, EndsAt: end, GeneratedAt: now,
		Ticks: projected, Truncated: truncated,
	}, nil
}

// compareStateTicks keeps causal lifecycle transitions ordered when storage
// timestamps tie. A maintenance terminal tick points at its deterministic
// activation tick through CausalTickID, so the start remains before the end
// without imposing a global lifecycle rank on unrelated observations.
func compareStateTicks(left, right domain.StateTick) int {
	if order := left.OccurredAt.Compare(right.OccurredAt); order != 0 {
		return order
	}
	if left.CausalTickID != nil && *left.CausalTickID == right.ID {
		return 1
	}
	if right.CausalTickID != nil && *right.CausalTickID == left.ID {
		return -1
	}
	if left.ID < right.ID {
		return -1
	}
	if left.ID > right.ID {
		return 1
	}
	return 0
}

// NormalizeStateTickHistoryQueryLimit returns a bounded storage limit with
// one extra row reserved for truncation detection.
func NormalizeStateTickHistoryQueryLimit(limit int) int {
	if limit <= 0 {
		return DefaultStateTickHistoryLimit
	}
	if limit > maxStateTickHistoryQueryLimit {
		return maxStateTickHistoryQueryLimit
	}
	return limit
}
