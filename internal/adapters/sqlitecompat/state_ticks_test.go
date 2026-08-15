package sqlitecompat

import (
	"context"
	"database/sql"
	"testing"
	"time"

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
			"system", at.Format(time.RFC3339Nano))
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
