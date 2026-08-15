package sqlitecompat

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	dbsqlite "github.com/araihu/xisnove/db/generated/sqlite"
	"github.com/araihu/xisnove/domain"
	_ "modernc.org/sqlite"
)

func TestStateTickRepositoryListsHalfOpenWindowInChronologicalOrder(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", "file:state-tick-history?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE state_ticks (
			id TEXT PRIMARY KEY,
			monitor_id TEXT NOT NULL,
			location_id TEXT,
			lifecycle TEXT NOT NULL,
			health TEXT NOT NULL,
			reason_code TEXT NOT NULL,
			action_id TEXT NOT NULL,
			user_action_id TEXT,
			actor_kind TEXT NOT NULL,
			actor_id TEXT,
			occurred_at TEXT NOT NULL,
			observation_id TEXT,
			causal_tick_id TEXT,
			causal_dependency_id TEXT
		)`); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	insert := func(id string, at time.Time, health, reason string) {
		t.Helper()
		_, err := db.Exec(`INSERT INTO state_ticks (
			id, monitor_id, lifecycle, health, reason_code, action_id,
			actor_kind, occurred_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, "monitor-1", "active", health, reason, "action-"+id,
			"system", formatStateTickTime(at))
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("tick-2", base.Add(time.Minute), "degraded", "probe_failure")
	insert("tick-1", base, "up", "probe_success")
	insert("tick-boundary", base.Add(2*time.Minute), "unknown", "dependency_unknown")

	repository := &stateTickRepository{queries: dbsqlite.New(db)}
	ticks, err := repository.ListStateTicks(context.Background(), "monitor-1", base, base.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 2 || ticks[0].ID != "tick-1" || ticks[1].ID != "tick-2" {
		t.Fatalf("ticks = %#v, want tick-1 then tick-2", ticks)
	}
	if ticks[0].Health != domain.HealthUp || ticks[1].Health != domain.HealthDegraded {
		t.Fatalf("health = %q, %q", ticks[0].Health, ticks[1].Health)
	}
}

func TestStateTickRepositoryRetainsNewestRowsWithSubMillisecondBoundaries(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", "file:state-tick-history-over-limit?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE state_ticks (
			id TEXT PRIMARY KEY,
			monitor_id TEXT NOT NULL,
			location_id TEXT,
			lifecycle TEXT NOT NULL,
			health TEXT NOT NULL,
			reason_code TEXT NOT NULL,
			action_id TEXT NOT NULL,
			user_action_id TEXT,
			actor_kind TEXT NOT NULL,
			actor_id TEXT,
			occurred_at TEXT NOT NULL,
			observation_id TEXT,
			causal_tick_id TEXT,
			causal_dependency_id TEXT
		)`); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 15, 12, 0, 0, 123456789, time.UTC)
	insert := func(id string, at time.Time) {
		t.Helper()
		_, err := db.Exec(`INSERT INTO state_ticks (
			id, monitor_id, lifecycle, health, reason_code, action_id,
			actor_kind, occurred_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, "monitor-1", "active", "up", "probe_success", "action-"+id,
			"system", formatStateTickTime(at))
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("tick-before", base.Add(-time.Nanosecond))
	insert("tick-start", base)
	insert("tick-middle", base.Add(time.Nanosecond))
	insert("tick-latest", base.Add(2*time.Nanosecond))
	insert("tick-end", base.Add(3*time.Nanosecond))

	repository := &stateTickRepository{queries: dbsqlite.New(db)}
	ticks, err := repository.ListStateTicks(context.Background(), "monitor-1", base, base.Add(3*time.Nanosecond), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 2 || ticks[0].ID != "tick-middle" || ticks[1].ID != "tick-latest" {
		t.Fatalf("ticks = %#v, want newest two in chronological order", ticks)
	}
	if !ticks[0].OccurredAt.Equal(base.Add(time.Nanosecond)) || !ticks[1].OccurredAt.Equal(base.Add(2*time.Nanosecond)) {
		t.Fatalf("timestamps = %s, %s, want sub-millisecond precision preserved", ticks[0].OccurredAt, ticks[1].OccurredAt)
	}
}

func TestStateTickRepositoryAppendsAndDeduplicatesWithProvenance(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", "file:state-tick-history-append?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE state_ticks (
			id TEXT PRIMARY KEY,
			monitor_id TEXT NOT NULL,
			location_id TEXT,
			lifecycle TEXT NOT NULL,
			health TEXT NOT NULL,
			reason_code TEXT NOT NULL,
			action_id TEXT NOT NULL,
			user_action_id TEXT,
			actor_kind TEXT NOT NULL,
			actor_id TEXT,
			occurred_at TEXT NOT NULL,
			observation_id TEXT,
			causal_tick_id TEXT,
			causal_dependency_id TEXT
		)`); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 15, 12, 0, 0, 123456789, time.UTC)
	userActionID := "user-action-1"
	observationID := "observation-1"
	causalDependencyID := "dependency-1"
	locationID := domain.LocationID("location-1")
	tick, err := domain.NewStateTick(domain.NewStateTickParams{
		ID: "tick-append", MonitorID: "monitor-1", LocationID: &locationID,
		Lifecycle: domain.MonitorLifecyclePaused, Health: domain.HealthUnknown,
		ReasonCode: domain.StateTickReasonPausedByUser, ActionID: "action-1",
		UserActionID: &userActionID,
		Actor:        domain.StateTickActor{Kind: domain.StateTickActorUser, ID: "user-1"},
		OccurredAt:   base, ObservationID: &observationID,
		CausalDependencyID: &causalDependencyID,
	})
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	var inserted bool
	err = store.WithinTx(context.Background(), func(repositories application.Repositories) error {
		var err error
		inserted, err = repositories.StateTickWriter.AppendStateTick(context.Background(), tick)
		return err
	})
	if err != nil || !inserted {
		t.Fatalf("first append = %v, %v; want inserted", inserted, err)
	}
	inserted, err = store.Repositories().StateTickWriter.AppendStateTick(context.Background(), tick)
	if err != nil || inserted {
		t.Fatalf("duplicate append = %v, %v; want idempotent no-op", inserted, err)
	}
	rollbackTick := tick.Clone()
	rollbackTick.ID = "tick-rollback"
	rollbackTick.ActionID = "action-rollback"
	rollbackErr := errors.New("force state tick rollback")
	err = store.WithinTx(context.Background(), func(repositories application.Repositories) error {
		if _, err := repositories.StateTickWriter.AppendStateTick(context.Background(), rollbackTick); err != nil {
			return err
		}
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("rollback error = %v, want %v", err, rollbackErr)
	}
	ticks, err := store.Repositories().StateTicks.ListStateTicks(context.Background(), "monitor-1", base, base.Add(time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 1 {
		t.Fatalf("ticks = %#v, want one persisted tick", ticks)
	}
	got := ticks[0]
	if got.ID != tick.ID || got.MonitorID != tick.MonitorID || got.LocationID == nil || *got.LocationID != locationID ||
		got.Lifecycle != tick.Lifecycle || got.Health != tick.Health || got.ReasonCode != tick.ReasonCode ||
		got.ActionID != tick.ActionID || got.UserActionID == nil || *got.UserActionID != userActionID ||
		got.Actor != tick.Actor || got.ObservationID == nil || *got.ObservationID != observationID ||
		got.CausalDependencyID == nil || *got.CausalDependencyID != causalDependencyID || !got.OccurredAt.Equal(base) {
		t.Fatalf("round-trip tick = %#v, want %#v", got, tick)
	}
}
