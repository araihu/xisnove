package application_test

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
	service := application.NewStalenessService(store, func() string {
		nextID++
		return fmt.Sprintf("stale-id-%d", nextID)
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
}
