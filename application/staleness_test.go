package application_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestStalenessSweepUsesDurableDeadlineAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "staleness.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(db)
	repositories := store.Repositories()
	now, err := repositories.Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	location, err := domain.NewLocation("location-1", "private", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Locations.Create(ctx, location); err != nil {
		t.Fatal(err)
	}
	monitor, err := domain.NewTCPMonitor(domain.NewTCPMonitorParams{
		ID: "monitor-1", Name: "postgres", Interval: time.Minute,
		Timeout: 5 * time.Second, FailureThreshold: 1, RecoveryThreshold: 1,
		TCP:       domain.TCPProbe{Host: "postgres.internal", Port: 5432},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Monitors.Create(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Monitors.AssignLocation(ctx, application.MonitorLocation{
		MonitorID: monitor.ID, LocationID: location.ID, Required: true,
	}); err != nil {
		t.Fatal(err)
	}
	locationHealth := domain.LocationHealth{
		MonitorID: monitor.ID, LocationID: location.ID, State: domain.HealthUp,
		LastObservedAt: now, LastTransitionAt: now, StaleAt: now.Add(time.Minute),
	}
	if err := repositories.Health.UpsertLocation(ctx, locationHealth); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Health.UpsertMonitor(ctx, domain.MonitorHealth{
		MonitorID: monitor.ID, State: domain.HealthUp, LastTransitionAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	nextID := 0
	var transitions []application.MonitorTransitionObservation
	service := application.NewStalenessServiceWithObserver(store, func() string {
		nextID++
		return fmt.Sprintf("stale-id-%d", nextID)
	}, func(observation application.MonitorTransitionObservation) {
		transitions = append(transitions, observation)
	})
	if marked, err := service.MarkDue(ctx, 100); err != nil || marked != 0 {
		t.Fatalf("before deadline marked=%d error=%v", marked, err)
	}

	locationHealth.StaleAt = now.Add(-time.Second)
	if err := repositories.Health.UpsertLocation(ctx, locationHealth); err != nil {
		t.Fatal(err)
	}
	if marked, err := service.MarkDue(ctx, 100); err != nil || marked != 1 {
		t.Fatalf("at deadline marked=%d error=%v", marked, err)
	}
	if marked, err := service.MarkDue(ctx, 100); err != nil || marked != 0 {
		t.Fatalf("second sweep marked=%d error=%v", marked, err)
	}
	gotLocation, err := repositories.Health.GetLocation(ctx, monitor.ID, location.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotMonitor, err := repositories.Health.GetMonitor(ctx, monitor.ID)
	if err != nil {
		t.Fatal(err)
	}
	incident, err := repositories.Incidents.GetActive(ctx, monitor.ID)
	if err != nil {
		t.Fatal(err)
	}
	var events int
	if err := db.QueryRow("SELECT COUNT(*) FROM incident_events").Scan(&events); err != nil {
		t.Fatal(err)
	}
	var audits int
	if err := db.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if gotLocation.State != domain.HealthUnknown ||
		!gotLocation.StaleAt.IsZero() ||
		gotMonitor.State != domain.HealthUnknown ||
		incident == nil ||
		incident.Severity != domain.IncidentWarning ||
		events != 1 ||
		audits != 1 {
		t.Fatalf(
			"location=%#v monitor=%#v incident=%#v events=%d audits=%d",
			gotLocation, gotMonitor, incident, events, audits,
		)
	}
	if len(transitions) != 1 || transitions[0].From != domain.HealthUp || transitions[0].To != domain.HealthUnknown {
		t.Fatalf("staleness transitions = %#v", transitions)
	}
}

func TestStalenessSweepSkipsDisabledPausedAndMaintainedCandidates(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(context.Context, port.Repositories, domain.Monitor, domain.Location, time.Time) error
	}{
		{name: "disabled monitor", setup: func(ctx context.Context, repositories port.Repositories, monitor domain.Monitor, _ domain.Location, now time.Time) error {
			_, err := repositories.ManagementCommands.DisableMonitor(ctx, monitor.ID, now)
			return err
		}},
		{name: "disabled location", setup: func(ctx context.Context, repositories port.Repositories, monitor domain.Monitor, location domain.Location, now time.Time) error {
			_, err := repositories.ManagementCommands.DisableLocation(ctx, location.ID, now)
			return err
		}},
		{name: "active maintenance", setup: func(ctx context.Context, repositories port.Repositories, monitor domain.Monitor, _ domain.Location, now time.Time) error {
			end := now.Add(time.Hour)
			interval, err := domain.NewMaintenanceInterval("maintenance-stale-skip", monitor.ID, now.Add(-time.Minute), &end, "planned")
			if err != nil {
				return err
			}
			interval.CreatedAt = now
			return repositories.Maintenance.Create(ctx, port.MaintenanceRecord{Interval: interval, UpdatedAt: now})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			db, store, repositories, monitor, location, now := newStaleCandidateFixture(t, ctx)
			defer db.Close()
			if err := test.setup(ctx, repositories, monitor, location, now); err != nil {
				t.Fatal(err)
			}
			before, err := repositories.StateTicks.ListStateTicks(ctx, monitor.ID, now.Add(-time.Minute), now.Add(time.Minute), 20)
			if err != nil {
				t.Fatal(err)
			}
			service := application.NewStalenessService(store, func() string { return "stale-skip-id" })
			marked, err := service.MarkDue(ctx, 100)
			if err != nil || marked != 0 {
				t.Fatalf("MarkDue() = %d, %v", marked, err)
			}
			after, err := repositories.StateTicks.ListStateTicks(ctx, monitor.ID, now.Add(-time.Minute), now.Add(time.Minute), 20)
			if err != nil {
				t.Fatal(err)
			}
			if len(after) != len(before) {
				t.Fatalf("state ticks changed from %#v to %#v", before, after)
			}
			locationHealth, err := repositories.Health.GetLocation(ctx, monitor.ID, location.ID)
			if err != nil {
				t.Fatal(err)
			}
			monitorHealth, err := repositories.Health.GetMonitor(ctx, monitor.ID)
			if err != nil {
				t.Fatal(err)
			}
			if locationHealth.State != domain.HealthUp || monitorHealth.State != domain.HealthUp {
				t.Fatalf("ineligible candidate projected unknown: location=%#v monitor=%#v", locationHealth, monitorHealth)
			}
		})
	}
}

func newStaleCandidateFixture(t *testing.T, ctx context.Context) (*sql.DB, port.Store, port.Repositories, domain.Monitor, domain.Location, time.Time) {
	t.Helper()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "staleness-ineligible.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		db.Close()
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(db)
	repositories := store.Repositories()
	now, err := repositories.Runs.DatabaseNow(ctx)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	location, err := domain.NewLocation("location-ineligible", "edge", now)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := repositories.Locations.Create(ctx, location); err != nil {
		db.Close()
		t.Fatal(err)
	}
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID: "monitor-ineligible", Name: "router", Interval: time.Minute, Timeout: time.Second,
		FailureThreshold: 1, RecoveryThreshold: 1,
		HTTP: domain.HTTPProbe{URL: "https://router.example.com"}, CreatedAt: now,
	})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := repositories.Monitors.Create(ctx, monitor); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := repositories.Monitors.AssignLocation(ctx, port.MonitorLocation{MonitorID: monitor.ID, LocationID: location.ID, Required: true}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := repositories.Health.UpsertLocation(ctx, domain.LocationHealth{
		MonitorID: monitor.ID, LocationID: location.ID, State: domain.HealthUp,
		LastObservedAt: now, LastTransitionAt: now, StaleAt: now.Add(-time.Second),
	}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := repositories.Health.UpsertMonitor(ctx, domain.MonitorHealth{
		MonitorID: monitor.ID, State: domain.HealthUp, LastTransitionAt: now,
	}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db, store, repositories, monitor, location, now
}
