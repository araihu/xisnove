package integration_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
	"github.com/araihu/xisnove/internal/adapters/ids"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestInitialStateTickSurvivesSQLiteReopen(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "state-ticks.db")
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)

	db, err := sqlitestore.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(db)
	configuration := application.NewConfigurationService(store, func() time.Time { return now }, ids.NewUUID)
	location, err := configuration.CreateLocation(ctx, application.CreateLocationCommand{Name: "primary"})
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	monitor, err := configuration.CreateMonitor(ctx, application.CreateMonitorCommand{
		Name: "primary HTTP", LocationID: location.ID, RequiredLocation: true,
		Interval: time.Minute, Timeout: 5 * time.Second, FailureThreshold: 3, RecoveryThreshold: 2,
		Probe: domain.ProbeDefinition{Kind: domain.MonitorKindHTTP, HTTP: domain.HTTPProbe{
			Method: "GET", URL: "https://example.test/health",
		}},
	})
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	assertInitialStateTick(t, store, monitor.ID, location.ID, now)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := sqlitestore.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := sqlitestore.Migrate(ctx, reopened); err != nil {
		t.Fatal(err)
	}
	assertInitialStateTick(t, sqlitestore.NewStore(reopened), monitor.ID, location.ID, now)
}

func assertInitialStateTick(
	t *testing.T,
	store application.Store,
	monitorID domain.MonitorID,
	locationID domain.LocationID,
	wantAt time.Time,
) {
	t.Helper()
	ticks, err := store.Repositories().StateTicks.ListStateTicks(
		context.Background(), monitorID, wantAt.Add(-time.Second), wantAt.Add(time.Second), 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 1 {
		t.Fatalf("state ticks = %d, want 1: %#v", len(ticks), ticks)
	}
	tick := ticks[0]
	if tick.MonitorID != monitorID || tick.LocationID == nil || *tick.LocationID != locationID ||
		tick.Lifecycle != domain.MonitorLifecycleActive || tick.Health != domain.HealthPending ||
		tick.ReasonCode != domain.StateTickReasonInitial || tick.Actor.Kind != domain.StateTickActorSystem ||
		!tick.OccurredAt.Equal(wantAt) {
		t.Fatalf("initial state tick = %#v", tick)
	}
}
