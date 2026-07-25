package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

func TestMaintenanceAdminLifecycleRejectsHistoryRewritesAndAuditsAtomically(t *testing.T) {
	ctx := context.Background()
	fixture := newProjectionFixture(t, ctx)
	now := fixture.now
	service := NewNotificationAdminService(NotificationAdminServiceConfig{
		Store: fixture.store, Now: func() time.Time { return now }, NewID: concurrentIDs(),
	})
	active, err := service.CreateMaintenance(ctx, CreateMaintenanceCommand{
		MonitorID: fixture.monitor.ID, StartsAt: now.Add(-time.Minute), Reason: "active upgrade",
	})
	if err != nil {
		t.Fatal(err)
	}
	ended, err := service.EndMaintenance(ctx, active.Interval.ID)
	if err != nil || ended.Interval.EndsAt == nil || !ended.Interval.EndsAt.Equal(now) {
		t.Fatalf("EndMaintenance() = %#v, %v", ended, err)
	}
	again, err := service.EndMaintenance(ctx, active.Interval.ID)
	if err != nil || again.Interval.EndsAt == nil || !again.Interval.EndsAt.Equal(now) {
		t.Fatalf("idempotent EndMaintenance() = %#v, %v", again, err)
	}

	future, err := service.CreateMaintenance(ctx, CreateMaintenanceCommand{
		MonitorID: fixture.monitor.ID, StartsAt: now.Add(time.Hour), Reason: "future upgrade",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.EndMaintenance(ctx, future.Interval.ID); err == nil || !isMaintenanceStartValidation(err) {
		t.Fatalf("future EndMaintenance() error = %v", err)
	}
	if err := service.DeleteMaintenance(ctx, future.Interval.ID); err != nil {
		t.Fatal(err)
	}
	var audits int
	if err := fixture.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE subject_kind = 'maintenance'`).Scan(&audits); err != nil || audits != 4 {
		t.Fatalf("maintenance audits = %d, %v", audits, err)
	}

	rollback, err := service.CreateMaintenance(ctx, CreateMaintenanceCommand{
		MonitorID: fixture.monitor.ID, StartsAt: now.Add(-time.Hour), Reason: "rollback",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.ExecContext(ctx, `
		CREATE TRIGGER fail_maintenance_audit BEFORE INSERT ON audit_events
		WHEN NEW.kind = 'maintenance.ended'
		BEGIN SELECT RAISE(ABORT, 'injected audit failure'); END
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.EndMaintenance(ctx, rollback.Interval.ID); err == nil {
		t.Fatal("EndMaintenance succeeded after audit failure")
	}
	stored, err := service.GetMaintenance(ctx, rollback.Interval.ID)
	if err != nil || stored.Interval.EndsAt != nil {
		t.Fatalf("rolled back maintenance = %#v, %v", stored, err)
	}
}

func isMaintenanceStartValidation(err error) bool {
	var validation *ValidationError
	return errors.As(err, &validation) && strings.Contains(validation.Fields["maintenance.startsAt"], "before it starts")
}

func TestMaintenanceWorkerEmitsExactlyOneSyntheticTransitionAcrossReplicas(t *testing.T) {
	ctx := context.Background()
	fixture := newProjectionFixture(t, ctx)
	fixture.route.Actions = append(fixture.route.Actions, domain.NotificationMaintenanceEnded)
	if changed, err := fixture.repositories.NotificationRoutes.Update(ctx, fixture.route); err != nil || !changed {
		t.Fatalf("update route = %v, %v", changed, err)
	}
	end := fixture.now.Add(time.Minute)
	interval, err := domain.NewMaintenanceInterval(
		"maintenance-ended-1", fixture.monitor.ID, fixture.now.Add(-time.Minute), &end, "router upgrade",
	)
	if err != nil {
		t.Fatal(err)
	}
	interval.CreatedAt = fixture.now.Add(-time.Minute)
	if err := fixture.repositories.Maintenance.Create(ctx, port.MaintenanceRecord{Interval: interval, UpdatedAt: fixture.now}); err != nil {
		t.Fatal(err)
	}
	projectState(t, ctx, fixture, domain.HealthDown, fixture.now, true, monotonicIDs())
	before := outboxRecords(t, ctx, fixture)
	if len(before) != 1 || before[0].State != domain.DeliverySuppressed {
		t.Fatalf("suppressed outbox = %#v", before)
	}

	ids := concurrentIDs()
	first := newMaintenanceWorker(t, fixture.store, "maintenance-worker-1", ids)
	second := newMaintenanceWorker(t, fixture.store, "maintenance-worker-2", ids)
	var group sync.WaitGroup
	counts := make(chan int, 2)
	errorsFound := make(chan error, 2)
	for _, worker := range []*MaintenanceWorker{first, second} {
		group.Add(1)
		go func(worker *MaintenanceWorker) {
			defer group.Done()
			count, err := worker.RunOnce(ctx)
			counts <- count
			errorsFound <- err
		}(worker)
	}
	group.Wait()
	close(counts)
	close(errorsFound)
	total := 0
	for count := range counts {
		total += count
	}
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	if total != 1 {
		t.Fatalf("processed intervals = %d", total)
	}
	after := outboxRecords(t, ctx, fixture)
	if len(after) != 2 || after[0].RenderSnapshot.Action != domain.NotificationMaintenanceEnded || after[0].State != domain.DeliveryPending {
		t.Fatalf("post-maintenance outbox = %#v", after)
	}
	stored, err := fixture.repositories.Maintenance.Get(ctx, interval.ID)
	if err != nil || !stored.Interval.EndedNotificationSent || stored.EndedNotificationSentAt == nil {
		t.Fatalf("processed maintenance = %#v, %v", stored, err)
	}
	if count, err := first.RunOnce(ctx); err != nil || count != 0 {
		t.Fatalf("idempotent RunOnce() = %d, %v", count, err)
	}
}

func TestMaintenanceWorkerMarksRecoveredIntervalWithoutNotification(t *testing.T) {
	ctx := context.Background()
	fixture := newProjectionFixture(t, ctx)
	end := fixture.now.Add(-time.Minute)
	interval, err := domain.NewMaintenanceInterval(
		"maintenance-healthy", fixture.monitor.ID, fixture.now.Add(-time.Hour), &end, "completed",
	)
	if err != nil {
		t.Fatal(err)
	}
	interval.CreatedAt = fixture.now.Add(-time.Hour)
	if err := fixture.repositories.Maintenance.Create(ctx, port.MaintenanceRecord{Interval: interval, UpdatedAt: fixture.now}); err != nil {
		t.Fatal(err)
	}
	health, err := fixture.repositories.Health.GetMonitor(ctx, fixture.monitor.ID)
	if err != nil {
		t.Fatal(err)
	}
	health.State = domain.HealthUp
	if err := fixture.repositories.Health.UpsertMonitor(ctx, health); err != nil {
		t.Fatal(err)
	}
	worker := newMaintenanceWorker(t, fixture.store, "maintenance-worker", concurrentIDs())
	if count, err := worker.RunOnce(ctx); err != nil || count != 1 {
		t.Fatalf("RunOnce() = %d, %v", count, err)
	}
	if records := outboxRecords(t, ctx, fixture); len(records) != 0 {
		t.Fatalf("healthy outbox = %#v", records)
	}
	stored, err := fixture.repositories.Maintenance.Get(ctx, interval.ID)
	if err != nil || !stored.Interval.EndedNotificationSent {
		t.Fatalf("processed healthy maintenance = %#v, %v", stored, err)
	}
}

func TestMaintenanceWorkerRollsBackProjectionAndReleasesClaimOnFailure(t *testing.T) {
	ctx := context.Background()
	fixture := newProjectionFixture(t, ctx)
	fixture.route.Actions = append(fixture.route.Actions, domain.NotificationMaintenanceEnded)
	if changed, err := fixture.repositories.NotificationRoutes.Update(ctx, fixture.route); err != nil || !changed {
		t.Fatalf("update route = %v, %v", changed, err)
	}
	end := fixture.now.Add(-time.Hour)
	interval, err := domain.NewMaintenanceInterval(
		"maintenance-rollback", fixture.monitor.ID, fixture.now.Add(-2*time.Hour), &end, "rollback",
	)
	if err != nil {
		t.Fatal(err)
	}
	interval.CreatedAt = fixture.now.Add(-2 * time.Hour)
	if err := fixture.repositories.Maintenance.Create(ctx, port.MaintenanceRecord{Interval: interval, UpdatedAt: fixture.now}); err != nil {
		t.Fatal(err)
	}
	projectState(t, ctx, fixture, domain.HealthDown, fixture.now, true, monotonicIDs())
	if _, err := fixture.db.ExecContext(ctx, `
		CREATE TRIGGER fail_maintenance_projection BEFORE INSERT ON audit_events
		WHEN NEW.kind = 'incident.maintenance-ended'
		BEGIN SELECT RAISE(ABORT, 'injected maintenance projection failure'); END
	`); err != nil {
		t.Fatal(err)
	}
	worker := newMaintenanceWorker(t, fixture.store, "maintenance-worker", concurrentIDs())
	if _, err := worker.RunOnce(ctx); err == nil {
		t.Fatal("RunOnce succeeded after injected projection failure")
	}
	stored, err := fixture.repositories.Maintenance.Get(ctx, interval.ID)
	if err != nil || stored.EndClaimExpiresAt != nil || stored.Interval.EndedNotificationSent {
		t.Fatalf("released maintenance = %#v, %v", stored, err)
	}
	if records := outboxRecords(t, ctx, fixture); len(records) != 1 {
		t.Fatalf("rolled-back outbox = %#v", records)
	}
	var events int
	if err := fixture.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incident_events`).Scan(&events); err != nil || events != 1 {
		t.Fatalf("rolled-back events = %d, %v", events, err)
	}
	if _, err := fixture.db.ExecContext(ctx, `DROP TRIGGER fail_maintenance_projection`); err != nil {
		t.Fatal(err)
	}
	if count, err := worker.RunOnce(ctx); err != nil || count != 1 {
		t.Fatalf("recovered RunOnce() = %d, %v", count, err)
	}
	if records := outboxRecords(t, ctx, fixture); len(records) != 2 {
		t.Fatalf("recovered outbox = %#v", records)
	}
}

func newMaintenanceWorker(t *testing.T, store port.UnitOfWork, owner string, newID func() string) *MaintenanceWorker {
	t.Helper()
	worker, err := NewMaintenanceWorker(MaintenanceWorkerConfig{
		Store: store, Tokens: &workerTokenIssuer{}, NewID: newID, Owner: owner,
		BatchSize: 1, LeaseDuration: time.Second, PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}
