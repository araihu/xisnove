package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
	"github.com/araihu/xisnove/internal/adapters/postgres"
	"github.com/google/uuid"
)

func TestStateTickRepositoryRetainsNewestRowsWithSubMillisecondBoundaries(t *testing.T) {
	baseURL := os.Getenv("XISNOVE_TEST_POSTGRES_URL")
	if baseURL == "" {
		t.Skip("XISNOVE_TEST_POSTGRES_URL is not set")
	}
	databaseURL := newPostgresTestSchema(t, baseURL)
	ctx := context.Background()
	db, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := postgres.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}

	monitorID := uuid.New()
	createdAt := time.Date(2026, 8, 15, 12, 0, 0, 123456789, time.UTC)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO monitors (
			id, name, kind, interval_ms, timeout_ms, failure_threshold,
			recovery_threshold, http_json, enabled, next_run_at, created_at, updated_at
		) VALUES ($1, $2, 'http', 60000, 5000, 1, 1, $3, true, $4, $4, $4)
	`, monitorID, "state tick over-limit", `{"method":"GET","url":"https://example.test/health"}`, createdAt); err != nil {
		t.Fatal(err)
	}
	writer := postgres.NewStore(db).Repositories().StateTickWriter
	if writer == nil {
		t.Fatal("state tick writer is not wired")
	}
	insert := func(id uuid.UUID, at time.Time) {
		t.Helper()
		tick, err := domain.NewStateTick(domain.NewStateTickParams{
			ID: id.String(), MonitorID: domain.MonitorID(monitorID.String()),
			Lifecycle: domain.MonitorLifecycleActive, Health: domain.HealthUp,
			ReasonCode: domain.StateTickReasonProbeSuccess, ActionID: uuid.NewString(),
			Actor: domain.StateTickActor{Kind: domain.StateTickActorSystem}, OccurredAt: at,
		})
		if err != nil {
			t.Fatal(err)
		}
		inserted, err := writer.AppendStateTick(ctx, tick)
		if err != nil || !inserted {
			t.Fatalf("append state tick = %v, %v; want inserted", inserted, err)
		}
	}
	base := createdAt
	insert(uuid.New(), base.Add(-time.Nanosecond))
	startID := uuid.New()
	insert(startID, base)
	middleID := uuid.New()
	insert(middleID, base.Add(time.Nanosecond))
	latestID := uuid.New()
	insert(latestID, base.Add(2*time.Nanosecond))
	insert(uuid.New(), base.Add(3*time.Nanosecond))

	repository := postgres.NewStore(db).Repositories().StateTicks
	ticks, err := repository.ListStateTicks(ctx, domain.MonitorID(monitorID.String()), base, base.Add(3*time.Nanosecond), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 2 || ticks[0].ID != middleID.String() || ticks[1].ID != latestID.String() {
		t.Fatalf("ticks = %#v, want newest two in chronological order", ticks)
	}
	if !ticks[0].OccurredAt.Equal(base.Add(time.Nanosecond)) || !ticks[1].OccurredAt.Equal(base.Add(2*time.Nanosecond)) {
		t.Fatalf("timestamps = %s, %s, want sub-millisecond precision preserved", ticks[0].OccurredAt, ticks[1].OccurredAt)
	}
}

func TestPostgresStateHistoryOrdersEqualTimestampCausalGroupBeforeLimit(t *testing.T) {
	baseURL := os.Getenv("XISNOVE_TEST_POSTGRES_URL")
	if baseURL == "" {
		t.Skip("XISNOVE_TEST_POSTGRES_URL is not set")
	}
	databaseURL := newPostgresTestSchema(t, baseURL)
	ctx := context.Background()
	db, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := postgres.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	monitorID := uuid.MustParse("00000000-0000-4000-8000-000000000099")
	store := postgres.NewStore(db)
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID: domain.MonitorID(monitorID.String()), Name: "state history", Interval: time.Minute,
		Timeout: 5 * time.Second, FailureThreshold: 1, RecoveryThreshold: 1,
		HTTP: domain.HTTPProbe{URL: "https://example.test/health"}, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Repositories().Monitors.Create(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	at := now.Add(-time.Minute)
	parentID := "ffffffff-ffff-4fff-8fff-ffffffffffff"
	childID := "00000000-0000-4000-8000-000000000001"
	grandchildID := "88888888-8888-4888-8888-888888888888"
	parent := newPostgresHistoryTick(t, parentID, monitor.ID, at, nil)
	child := newPostgresHistoryTick(t, childID, monitor.ID, at, &parent.ID)
	grandchild := newPostgresHistoryTick(t, grandchildID, monitor.ID, at, &child.ID)
	writer := store.Repositories().StateTickWriter
	for _, tick := range []domain.StateTick{parent, child, grandchild} {
		inserted, err := writer.AppendStateTick(ctx, tick)
		if err != nil || !inserted {
			t.Fatalf("append %q = %v, %v", tick.ID, inserted, err)
		}
	}

	service := application.NewStateTickHistoryServiceWithClock(store, func() time.Time { return now })
	for _, test := range []struct {
		limit int
		want  []string
	}{
		{limit: 1, want: []string{grandchildID}},
		{limit: 2, want: []string{childID, grandchildID}},
	} {
		t.Run(fmt.Sprintf("limit-%d", test.limit), func(t *testing.T) {
			view, err := service.GetMonitorStateHistory(ctx, monitor.ID, nil, nil, &test.limit)
			if err != nil {
				t.Fatal(err)
			}
			if !view.Truncated || len(view.Ticks) != len(test.want) {
				t.Fatalf("view = %#v", view)
			}
			for index, wantID := range test.want {
				if view.Ticks[index].ID != wantID {
					t.Fatalf("tick %d = %q, want %q; view = %#v", index, view.Ticks[index].ID, wantID, view.Ticks)
				}
			}
		})
	}
}

func newPostgresHistoryTick(t *testing.T, id string, monitorID domain.MonitorID, at time.Time, causalTickID *string) domain.StateTick {
	t.Helper()
	tick, err := domain.NewStateTick(domain.NewStateTickParams{
		ID: id, MonitorID: monitorID, Lifecycle: domain.MonitorLifecycleActive,
		Health: domain.HealthUnknown, ReasonCode: domain.StateTickReasonMaintenance,
		ActionID: uuid.NewString(), Actor: domain.StateTickActor{Kind: domain.StateTickActorSystem},
		OccurredAt: at, CausalTickID: causalTickID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return tick
}
