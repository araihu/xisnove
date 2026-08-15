package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/araihu/xisnove/domain"
)

func TestStateTickHistoryServiceBoundsAndOrdersTicks(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	ticks := []domain.StateTick{
		newHistoryTestTick(t, "tick-3", "monitor-1", now.Add(-30*time.Minute), domain.HealthUnknown, domain.StateTickReasonDependencyUnknown),
		newHistoryTestTick(t, "tick-1", "monitor-1", now.Add(-2*time.Hour), domain.HealthUp, domain.StateTickReasonProbeSuccess),
		newHistoryTestTick(t, "tick-2", "monitor-1", now.Add(-time.Hour), domain.HealthDegraded, domain.StateTickReasonProbeFailure),
	}
	repository := &stateTickHistoryRepository{ticks: ticks}
	store := &stateTickHistoryStore{repositories: Repositories{
		Monitors:   &stateTickHistoryMonitorRepository{},
		StateTicks: repository,
	}}
	service := NewStateTickHistoryServiceWithClock(store, func() time.Time { return now })

	view, err := service.GetMonitorStateHistory(context.Background(), "monitor-1", nil, nil, intPointer(2))
	if err != nil {
		t.Fatal(err)
	}
	if !view.StartsAt.Equal(now.Add(-3*time.Hour)) || !view.EndsAt.Equal(now) || !view.GeneratedAt.Equal(now) {
		t.Fatalf("window = %#v", view)
	}
	if !view.Truncated || len(view.Ticks) != 2 {
		t.Fatalf("truncation = %v, ticks = %d", view.Truncated, len(view.Ticks))
	}
	if view.Ticks[0].ID != "tick-2" || view.Ticks[1].ID != "tick-3" {
		t.Fatalf("ticks = %#v, want newest deterministic order", view.Ticks)
	}
	if repository.limit != 3 {
		t.Fatalf("repository limit = %d, want one extra row", repository.limit)
	}
}

func TestStateTickHistoryServiceRejectsInvalidWindowAndLimit(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	store := &stateTickHistoryStore{repositories: Repositories{
		Monitors:   &stateTickHistoryMonitorRepository{},
		StateTicks: &stateTickHistoryRepository{},
	}}
	service := NewStateTickHistoryServiceWithClock(store, func() time.Time { return now })
	future := now.Add(time.Minute)
	tooOld := now.Add(-4 * time.Hour)
	justBefore := now.Add(-time.Minute)
	tests := []struct {
		name      string
		startsAt  *time.Time
		endsAt    *time.Time
		limit     *int
		wantField string
	}{
		{name: "future end", endsAt: &future, wantField: "endsAt"},
		{name: "reversed", startsAt: &future, endsAt: &justBefore, wantField: "window"},
		{name: "over-wide", startsAt: &tooOld, endsAt: &now, wantField: "window"},
		{name: "zero limit", limit: intPointer(0), wantField: "limit"},
		{name: "over limit", limit: intPointer(MaxStateTickHistoryLimit + 1), wantField: "limit"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.GetMonitorStateHistory(context.Background(), "monitor-1", test.startsAt, test.endsAt, test.limit)
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error = %v, want ValidationError", err)
			}
			if _, ok := validation.Fields[test.wantField]; !ok {
				t.Fatalf("fields = %#v, want %q", validation.Fields, test.wantField)
			}
		})
	}
}

func TestStateTickHistoryServiceRejectsRepositoryLeak(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	bad := newHistoryTestTick(t, "tick-1", "other-monitor", now.Add(-time.Minute), domain.HealthUp, domain.StateTickReasonProbeSuccess)
	store := &stateTickHistoryStore{repositories: Repositories{
		Monitors:   &stateTickHistoryMonitorRepository{},
		StateTicks: &stateTickHistoryRepository{ticks: []domain.StateTick{bad}},
	}}
	service := NewStateTickHistoryServiceWithClock(store, func() time.Time { return now })
	if _, err := service.GetMonitorStateHistory(context.Background(), "monitor-1", nil, nil, nil); !errors.Is(err, ErrInvalidStateTickHistory) {
		t.Fatalf("error = %v, want ErrInvalidStateTickHistory", err)
	}
}

func TestStateTickHistoryServiceRejectsDuplicateTickIDs(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	tick := newHistoryTestTick(t, "tick-1", "monitor-1", now.Add(-time.Minute), domain.HealthUp, domain.StateTickReasonProbeSuccess)
	store := &stateTickHistoryStore{repositories: Repositories{
		Monitors: &stateTickHistoryMonitorRepository{}, StateTicks: &stateTickHistoryRepository{
			ticks: []domain.StateTick{tick, tick},
		},
	}}
	service := NewStateTickHistoryServiceWithClock(store, func() time.Time { return now })
	if _, err := service.GetMonitorStateHistory(context.Background(), "monitor-1", nil, nil, nil); !errors.Is(err, ErrInvalidStateTickHistory) {
		t.Fatalf("error = %v, want ErrInvalidStateTickHistory", err)
	}
}

func TestStateTickHistoryServicePreservesProvenance(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	userActionID := "user-action-1"
	causalDependencyID := "dependency-1"
	tick, err := domain.NewStateTick(domain.NewStateTickParams{
		ID: "tick-1", MonitorID: "monitor-1", Lifecycle: domain.MonitorLifecyclePaused,
		Health: domain.HealthUnknown, ReasonCode: domain.StateTickReasonPausedByUser,
		ActionID: "action-1", UserActionID: &userActionID,
		Actor:      domain.StateTickActor{Kind: domain.StateTickActorUser, ID: "user-1"},
		OccurredAt: now.Add(-time.Minute), CausalDependencyID: &causalDependencyID,
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := &stateTickHistoryRepository{ticks: []domain.StateTick{tick}}
	store := &stateTickHistoryStore{repositories: Repositories{
		Monitors: &stateTickHistoryMonitorRepository{}, StateTicks: repository,
	}}
	view, err := NewStateTickHistoryServiceWithClock(store, func() time.Time { return now }).GetMonitorStateHistory(
		context.Background(), "monitor-1", nil, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Ticks) != 1 || view.Ticks[0].ReasonCode != domain.StateTickReasonPausedByUser ||
		view.Ticks[0].Actor.Kind != domain.StateTickActorUser || view.Ticks[0].Actor.ID != "user-1" ||
		view.Ticks[0].UserActionID == nil || *view.Ticks[0].UserActionID != userActionID ||
		view.Ticks[0].CausalDependencyID == nil || *view.Ticks[0].CausalDependencyID != causalDependencyID {
		t.Fatalf("provenance = %#v", view.Ticks)
	}
	*repository.ticks[0].UserActionID = "mutated-after-query"
	if *view.Ticks[0].UserActionID != userActionID {
		t.Fatal("history view aliases repository provenance")
	}
}

func newHistoryTestTick(t *testing.T, id string, monitorID domain.MonitorID, at time.Time, health domain.HealthState, reason domain.StateTickReasonCode) domain.StateTick {
	t.Helper()
	tick, err := domain.NewStateTick(domain.NewStateTickParams{
		ID: id, MonitorID: monitorID, Lifecycle: domain.MonitorLifecycleActive,
		Health: health, ReasonCode: reason, ActionID: "action-" + id,
		Actor: domain.StateTickActor{Kind: domain.StateTickActorSystem}, OccurredAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	return tick
}

func intPointer(value int) *int { return &value }

type stateTickHistoryStore struct{ repositories Repositories }

func (s *stateTickHistoryStore) View(ctx context.Context, fn func(context.Context, Repositories) error) error {
	return fn(ctx, s.repositories)
}

func (s *stateTickHistoryStore) Transact(ctx context.Context, fn func(context.Context, Repositories) error) error {
	return fn(ctx, s.repositories)
}

func (s *stateTickHistoryStore) Repositories() Repositories { return s.repositories }

func (s *stateTickHistoryStore) WithinTx(ctx context.Context, fn func(Repositories) error) error {
	return fn(s.repositories)
}

type stateTickHistoryRepository struct {
	ticks []domain.StateTick
	limit int
}

func (r *stateTickHistoryRepository) ListStateTicks(_ context.Context, _ domain.MonitorID, _ time.Time, _ time.Time, limit int) ([]domain.StateTick, error) {
	r.limit = limit
	return append([]domain.StateTick(nil), r.ticks...), nil
}

type stateTickHistoryMonitorRepository struct{}

func (*stateTickHistoryMonitorRepository) Create(context.Context, domain.Monitor) error {
	return nil
}
func (*stateTickHistoryMonitorRepository) Get(_ context.Context, id domain.MonitorID) (domain.Monitor, error) {
	return domain.Monitor{ID: id}, nil
}
func (*stateTickHistoryMonitorRepository) AssignLocation(context.Context, MonitorLocation) error {
	return nil
}
func (*stateTickHistoryMonitorRepository) GetAssignment(context.Context, domain.MonitorID) (MonitorLocation, error) {
	return MonitorLocation{}, ErrNotFound
}
func (*stateTickHistoryMonitorRepository) ListDue(context.Context, time.Time, int) ([]DueMonitor, error) {
	return nil, nil
}
func (*stateTickHistoryMonitorRepository) AdvanceNextRun(context.Context, domain.MonitorID, time.Time, time.Time) (bool, error) {
	return false, nil
}
