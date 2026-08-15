package integration_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestSQLiteStateHistoryOrdersEqualTimestampCausalGroupBeforeLimit(t *testing.T) {
	ctx := context.Background()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "state-history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(db)
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID: "monitor-state-history", Name: "state history", Interval: time.Minute,
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
	parent := newSQLiteHistoryTick(t, "z-parent", monitor.ID, at, nil)
	child := newSQLiteHistoryTick(t, "a-child", monitor.ID, at, &parent.ID)
	grandchild := newSQLiteHistoryTick(t, "m-grandchild", monitor.ID, at, &child.ID)
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
		{limit: 1, want: []string{"m-grandchild"}},
		{limit: 2, want: []string{"a-child", "m-grandchild"}},
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

func newSQLiteHistoryTick(t *testing.T, id string, monitorID domain.MonitorID, at time.Time, causalTickID *string) domain.StateTick {
	t.Helper()
	tick, err := domain.NewStateTick(domain.NewStateTickParams{
		ID: id, MonitorID: monitorID, Lifecycle: domain.MonitorLifecycleActive,
		Health: domain.HealthUnknown, ReasonCode: domain.StateTickReasonMaintenance,
		ActionID: "action-" + id, Actor: domain.StateTickActor{Kind: domain.StateTickActorSystem},
		OccurredAt: at, CausalTickID: causalTickID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return tick
}
