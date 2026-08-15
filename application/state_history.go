package application

import (
	"container/heap"
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
	// Fetch the full public bound plus one row before causal ordering and
	// truncation. Storage ordering cannot safely decide which equal-timestamp
	// causal parents are needed for the newest public rows.
	maxStateTickHistoryQueryLimit = MaxStateTickHistoryLimit + 1
)

// ErrInvalidStateTickHistory indicates a malformed history query or a
// repository result that violates the immutable history contract.
var ErrInvalidStateTickHistory = errors.New("invalid state tick history")

// StateTickHistoryView is a bounded immutable history snapshot for one
// monitor. Ticks are chronological; equal-timestamp groups are topologically
// ordered by causal parent/child relation with stable ID tie-breaking.
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
		// Fetch before truncation so a storage-level timestamp/ID order cannot
		// omit a causal parent needed to order the newest public rows.
		var err error
		ticks, err = repositories.StateTicks.ListStateTicks(
			ctx, monitorID, start, end, maxStateTickHistoryQueryLimit,
		)
		return err
	})
	if err != nil {
		return StateTickHistoryView{}, err
	}
	if len(ticks) > maxStateTickHistoryQueryLimit {
		return StateTickHistoryView{}, ErrInvalidStateTickHistory
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
	projected, err = orderStateTicks(projected)
	if err != nil {
		return StateTickHistoryView{}, err
	}
	truncated := len(projected) > requestedLimit
	if truncated {
		projected = projected[len(projected)-requestedLimit:]
	}
	return StateTickHistoryView{
		MonitorID: monitorID, StartsAt: start, EndsAt: end, GeneratedAt: now,
		Ticks: projected, Truncated: truncated,
	}, nil
}

// orderStateTicks orders chronological groups and topologically resolves
// causal edges inside each equal-timestamp group. Kahn's algorithm uses tick
// IDs as the ready-queue tie-break, giving a strict deterministic order while
// ensuring every causal parent precedes its child. A cycle is malformed
// immutable history and is rejected rather than being silently reordered.
func orderStateTicks(ticks []domain.StateTick) ([]domain.StateTick, error) {
	slices.SortStableFunc(ticks, func(left, right domain.StateTick) int {
		return left.OccurredAt.Compare(right.OccurredAt)
	})
	ordered := make([]domain.StateTick, 0, len(ticks))
	for start := 0; start < len(ticks); {
		end := start + 1
		for end < len(ticks) && ticks[end].OccurredAt.Equal(ticks[start].OccurredAt) {
			end++
		}
		group, err := orderEqualStateTickGroup(ticks[start:end])
		if err != nil {
			return nil, err
		}
		ordered = append(ordered, group...)
		start = end
	}
	return ordered, nil
}

func orderEqualStateTickGroup(group []domain.StateTick) ([]domain.StateTick, error) {
	byID := make(map[string]int, len(group))
	for index, tick := range group {
		byID[tick.ID] = index
	}
	children := make([][]int, len(group))
	indegree := make([]int, len(group))
	for childIndex, tick := range group {
		if tick.CausalTickID == nil {
			continue
		}
		parentIndex, exists := byID[*tick.CausalTickID]
		if !exists {
			// The causal parent may be outside the requested window or the
			// bounded storage fetch. Chronological ordering still applies.
			continue
		}
		children[parentIndex] = append(children[parentIndex], childIndex)
		indegree[childIndex]++
	}

	ready := &stateTickReadyHeap{ticks: group}
	for index, degree := range indegree {
		if degree == 0 {
			ready.indices = append(ready.indices, index)
		}
	}
	heap.Init(ready)
	ordered := make([]domain.StateTick, 0, len(group))
	for ready.Len() > 0 {
		index := heap.Pop(ready).(int)
		ordered = append(ordered, group[index])
		for _, childIndex := range children[index] {
			indegree[childIndex]--
			if indegree[childIndex] == 0 {
				heap.Push(ready, childIndex)
			}
		}
	}
	if len(ordered) != len(group) {
		return nil, ErrInvalidStateTickHistory
	}
	return ordered, nil
}

type stateTickReadyHeap struct {
	ticks   []domain.StateTick
	indices []int
}

func (h stateTickReadyHeap) Len() int { return len(h.indices) }

func (h stateTickReadyHeap) Less(left, right int) bool {
	return h.ticks[h.indices[left]].ID < h.ticks[h.indices[right]].ID
}

func (h stateTickReadyHeap) Swap(left, right int) {
	h.indices[left], h.indices[right] = h.indices[right], h.indices[left]
}

func (h *stateTickReadyHeap) Push(value any) {
	h.indices = append(h.indices, value.(int))
}

func (h *stateTickReadyHeap) Pop() any {
	last := len(h.indices) - 1
	value := h.indices[last]
	h.indices = h.indices[:last]
	return value
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
