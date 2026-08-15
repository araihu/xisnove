package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

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
