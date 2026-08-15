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

func TestMaintenanceStartTickPreservesUserPrincipalAndAction(t *testing.T) {
	ctx := context.Background()
	fixture := newProjectionFixture(t, ctx)
	service := NewNotificationAdminService(NotificationAdminServiceConfig{
		Store: fixture.store, Now: func() time.Time { return fixture.now }, NewID: concurrentIDs(),
	})
	principal := Principal{Kind: PrincipalAdmin, SubjectID: "admin-1"}
	if _, err := service.CreateMaintenance(ctx, CreateMaintenanceCommand{
		MonitorID: fixture.monitor.ID, StartsAt: fixture.now.Add(-time.Minute),
		Reason: "user requested", Principal: principal,
	}); err != nil {
		t.Fatal(err)
	}
	ticks, err := fixture.repositories.StateTicks.ListStateTicks(
		ctx, fixture.monitor.ID, fixture.now.Add(-time.Minute), fixture.now.Add(time.Minute), 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 1 {
		t.Fatalf("maintenance ticks = %#v", ticks)
	}
	tick := ticks[0]
	if tick.ReasonCode != domain.StateTickReasonMaintenance ||
		tick.Actor != (domain.StateTickActor{Kind: domain.StateTickActorUser, ID: principal.SubjectID}) ||
		tick.UserActionID == nil || tick.Lifecycle != domain.MonitorLifecyclePaused {
		t.Fatalf("maintenance tick provenance = %#v", tick)
	}
}

func TestFutureMaintenanceDoesNotRecordBeforeActivation(t *testing.T) {
	ctx := context.Background()
	fixture := newProjectionFixture(t, ctx)
	service := NewNotificationAdminService(NotificationAdminServiceConfig{
		Store: fixture.store, Now: func() time.Time { return fixture.now }, NewID: concurrentIDs(),
	})
	startsAt := fixture.now.Add(time.Hour)
	if _, err := service.CreateMaintenance(ctx, CreateMaintenanceCommand{
		MonitorID: fixture.monitor.ID, StartsAt: startsAt, Reason: "future user request",
		Principal: Principal{Kind: PrincipalAdmin, SubjectID: "admin-1"},
	}); err != nil {
		t.Fatal(err)
	}
	ticks, err := fixture.repositories.StateTicks.ListStateTicks(
		ctx, fixture.monitor.ID, fixture.now, startsAt.Add(time.Minute), 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 0 {
		t.Fatalf("future maintenance ticks = %#v", ticks)
	}
}

func TestFutureMaintenanceWorkerPreservesAuthenticatedProvenanceAfterRestart(t *testing.T) {
	ctx := context.Background()
	fixture := newProjectionFixture(t, ctx)
	createdAt, err := fixture.repositories.Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	creationNow := createdAt.Add(-2 * time.Minute)
	startsAt := createdAt.Add(-time.Minute)
	principal := Principal{Kind: PrincipalAdmin, SubjectID: "admin-1"}
	service := NewNotificationAdminService(NotificationAdminServiceConfig{
		Store: fixture.store, Now: func() time.Time { return creationNow }, NewID: concurrentIDs(),
	})
	record, err := service.CreateMaintenance(ctx, CreateMaintenanceCommand{
		MonitorID: fixture.monitor.ID, StartsAt: startsAt, Reason: "future user request", Principal: principal,
	})
	if err != nil {
		t.Fatal(err)
	}

	// A fresh worker instance represents a process restart: provenance must be
	// recovered from durable audit state, not from the creating request.
	worker := newMaintenanceWorker(t, fixture.store, "maintenance-worker-restarted", concurrentIDs())
	if count, err := worker.RunOnce(ctx); err != nil || count != 1 {
		t.Fatalf("restart worker RunOnce() = %d, %v", count, err)
	}
	workerNow, err := fixture.repositories.Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ticks, err := fixture.repositories.StateTicks.ListStateTicks(
		ctx, fixture.monitor.ID, createdAt.Add(-time.Minute), workerNow.Add(time.Minute), 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 1 {
		t.Fatalf("future activation ticks = %#v", ticks)
	}
	tick := ticks[0]
	if tick.Actor != (domain.StateTickActor{Kind: domain.StateTickActorUser, ID: principal.SubjectID}) ||
		tick.UserActionID == nil || tick.Lifecycle != domain.MonitorLifecyclePaused || tick.ReasonCode != domain.StateTickReasonMaintenance {
		t.Fatalf("future activation provenance = %#v", tick)
	}

	// The record remains addressable after activation; this assertion keeps the
	// test tied to the same durable maintenance subject used for the lookup.
	if stored, err := fixture.repositories.Maintenance.Get(ctx, record.Interval.ID); err != nil || stored.Interval.ID != record.Interval.ID {
		t.Fatalf("activated maintenance = %#v, %v", stored, err)
	}
}

func TestMaintenanceWorkerEmitsStartBeforeShortLivedExpiry(t *testing.T) {
	ctx := context.Background()
	fixture := newProjectionFixture(t, ctx)
	now, err := fixture.repositories.Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	endsAt := now.Add(-time.Minute)
	interval, err := domain.NewMaintenanceInterval(
		"maintenance-short-lived", fixture.monitor.ID, now.Add(-2*time.Minute), &endsAt, "short-lived",
	)
	if err != nil {
		t.Fatal(err)
	}
	interval.CreatedAt = now.Add(-2 * time.Minute)
	if err := fixture.repositories.Maintenance.Create(ctx, port.MaintenanceRecord{Interval: interval, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	worker := newMaintenanceWorker(t, fixture.store, "maintenance-worker-short-lived", concurrentIDs())
	if count, err := worker.RunOnce(ctx); err != nil || count != 1 {
		t.Fatalf("short-lived RunOnce() = %d, %v", count, err)
	}
	workerNow, err := fixture.repositories.Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ticks, err := fixture.repositories.StateTicks.ListStateTicks(
		ctx, fixture.monitor.ID, now.Add(-3*time.Minute), workerNow.Add(time.Minute), 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 2 {
		t.Fatalf("short-lived lifecycle ticks = %#v", ticks)
	}
	var startAt, expiryAt time.Time
	for _, tick := range ticks {
		if tick.ReasonCode != domain.StateTickReasonMaintenance {
			t.Fatalf("short-lived tick reasons = %#v", ticks)
		}
		switch tick.Lifecycle {
		case domain.MonitorLifecyclePaused:
			startAt = tick.OccurredAt
		case domain.MonitorLifecycleActive:
			expiryAt = tick.OccurredAt
		}
	}
	if startAt.IsZero() || expiryAt.IsZero() || startAt.After(expiryAt) {
		t.Fatalf("short-lived lifecycle times = %#v", ticks)
	}
}

func TestCancelledFutureMaintenanceDoesNotActivate(t *testing.T) {
	ctx := context.Background()
	fixture := newProjectionFixture(t, ctx)
	now, err := fixture.repositories.Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	service := NewNotificationAdminService(NotificationAdminServiceConfig{
		Store: fixture.store, Now: func() time.Time { return now }, NewID: concurrentIDs(),
	})
	record, err := service.CreateMaintenance(ctx, CreateMaintenanceCommand{
		MonitorID: fixture.monitor.ID, StartsAt: now.Add(time.Hour), Reason: "cancelled future",
		Principal: Principal{Kind: PrincipalAdmin, SubjectID: "admin-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteMaintenance(ctx, record.Interval.ID); err != nil {
		t.Fatal(err)
	}
	worker := newMaintenanceWorker(t, fixture.store, "maintenance-worker", concurrentIDs())
	if count, err := worker.RunOnce(ctx); err != nil || count != 0 {
		t.Fatalf("cancelled future RunOnce() = %d, %v", count, err)
	}
	ticks, err := fixture.repositories.StateTicks.ListStateTicks(
		ctx, fixture.monitor.ID, now.Add(-time.Minute), now.Add(2*time.Hour), 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 0 {
		t.Fatalf("cancelled future ticks = %#v", ticks)
	}
}

func TestMaintenanceEndTickPreservesUserPrincipalAndAction(t *testing.T) {
	ctx := context.Background()
	fixture := newProjectionFixture(t, ctx)
	service := NewNotificationAdminService(NotificationAdminServiceConfig{
		Store: fixture.store, Now: func() time.Time { return fixture.now }, NewID: concurrentIDs(),
	})
	principal := Principal{Kind: PrincipalAdmin, SubjectID: "admin-1"}
	record, err := service.CreateMaintenance(ctx, CreateMaintenanceCommand{
		MonitorID: fixture.monitor.ID, StartsAt: fixture.now.Add(-time.Minute), Reason: "user requested",
		Principal: principal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.EndMaintenance(ctx, record.Interval.ID, principal); err != nil {
		t.Fatal(err)
	}
	ticks, err := fixture.repositories.StateTicks.ListStateTicks(
		ctx, fixture.monitor.ID, fixture.now.Add(-time.Minute), fixture.now.Add(time.Minute), 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 2 {
		t.Fatalf("maintenance lifecycle ticks = %#v", ticks)
	}
	for _, tick := range ticks {
		if tick.Actor != (domain.StateTickActor{Kind: domain.StateTickActorUser, ID: principal.SubjectID}) || tick.UserActionID == nil {
			t.Fatalf("maintenance lifecycle provenance = %#v", ticks)
		}
	}
}

func TestEndingDueFutureMaintenanceAppendsStartBeforeTerminal(t *testing.T) {
	ctx := context.Background()
	fixture := newProjectionFixture(t, ctx)
	now, err := fixture.repositories.Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	creationNow := now.Add(-2 * time.Minute)
	createService := NewNotificationAdminService(NotificationAdminServiceConfig{
		Store: fixture.store, Now: func() time.Time { return creationNow }, NewID: concurrentIDs(),
	})
	createPrincipal := Principal{Kind: PrincipalAdmin, SubjectID: "creator-1"}
	record, err := createService.CreateMaintenance(ctx, CreateMaintenanceCommand{
		MonitorID: fixture.monitor.ID, StartsAt: now.Add(-time.Minute), Reason: "future request",
		Principal: createPrincipal,
	})
	if err != nil {
		t.Fatal(err)
	}

	endService := NewNotificationAdminService(NotificationAdminServiceConfig{
		Store: fixture.store, Now: func() time.Time { return now }, NewID: concurrentIDs(),
	})
	endPrincipal := Principal{Kind: PrincipalAdmin, SubjectID: "ender-1"}
	if _, err := endService.EndMaintenance(ctx, record.Interval.ID, endPrincipal); err != nil {
		t.Fatal(err)
	}

	ticks, err := fixture.repositories.StateTicks.ListStateTicks(
		ctx, fixture.monitor.ID, now.Add(-2*time.Minute), now.Add(time.Minute), 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 2 {
		t.Fatalf("manual end lifecycle ticks = %#v", ticks)
	}
	var start, terminal domain.StateTick
	for _, tick := range ticks {
		if tick.Lifecycle == domain.MonitorLifecyclePaused {
			start = tick
		}
		if tick.Lifecycle == domain.MonitorLifecycleActive {
			terminal = tick
		}
	}
	if start.ID == "" || terminal.ID == "" {
		t.Fatalf("manual end lifecycle ticks = %#v", ticks)
	}
	if start.Actor != (domain.StateTickActor{Kind: domain.StateTickActorUser, ID: createPrincipal.SubjectID}) || start.UserActionID == nil {
		t.Fatalf("manual end start provenance = %#v", start)
	}
	if terminal.Actor != (domain.StateTickActor{Kind: domain.StateTickActorUser, ID: endPrincipal.SubjectID}) || terminal.UserActionID == nil {
		t.Fatalf("manual end terminal provenance = %#v", terminal)
	}
	if terminal.CausalTickID == nil || *terminal.CausalTickID != start.ID {
		t.Fatalf("manual end causal ordering = %#v", ticks)
	}

	history := NewStateTickHistoryServiceWithClock(fixture.store, func() time.Time { return now.Add(time.Second) })
	view, err := history.GetMonitorStateHistory(ctx, fixture.monitor.ID, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Ticks) < 2 || view.Ticks[len(view.Ticks)-2].ID != start.ID || view.Ticks[len(view.Ticks)-1].ID != terminal.ID {
		t.Fatalf("manual end public ordering = %#v", view.Ticks)
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

func TestMaintenanceWorkerRecordsNaturalExpiryTick(t *testing.T) {
	ctx := context.Background()
	fixture := newProjectionFixture(t, ctx)
	end := fixture.now.Add(-time.Minute)
	interval, err := domain.NewMaintenanceInterval(
		"maintenance-expiry-tick", fixture.monitor.ID, fixture.now.Add(-time.Hour), &end, "expired",
	)
	if err != nil {
		t.Fatal(err)
	}
	interval.CreatedAt = fixture.now.Add(-time.Hour)
	if err := fixture.repositories.Maintenance.Create(ctx, port.MaintenanceRecord{Interval: interval, UpdatedAt: fixture.now}); err != nil {
		t.Fatal(err)
	}
	worker := newMaintenanceWorker(t, fixture.store, "maintenance-worker", concurrentIDs())
	if count, err := worker.RunOnce(ctx); err != nil || count != 1 {
		t.Fatalf("RunOnce() = %d, %v", count, err)
	}
	workerNow, err := fixture.repositories.Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ticks, err := fixture.repositories.StateTicks.ListStateTicks(
		ctx, fixture.monitor.ID, workerNow.Add(-time.Hour), workerNow.Add(time.Minute), 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	active, paused := 0, 0
	for _, tick := range ticks {
		if tick.ReasonCode != domain.StateTickReasonMaintenance || tick.Actor.Kind != domain.StateTickActorSystem {
			t.Fatalf("expiry tick = %#v", ticks)
		}
		switch tick.Lifecycle {
		case domain.MonitorLifecycleActive:
			active++
		case domain.MonitorLifecyclePaused:
			paused++
		}
	}
	if len(ticks) != 2 || active != 1 || paused != 1 {
		t.Fatalf("expiry tick = %#v", ticks)
	}
}

func TestMaintenanceWorkerActivatesDueMaintenanceAtEffectiveTime(t *testing.T) {
	ctx := context.Background()
	fixture := newProjectionFixture(t, ctx)
	now, err := fixture.repositories.Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	endsAt := now.Add(time.Hour)
	interval, err := domain.NewMaintenanceInterval(
		"maintenance-activation-tick", fixture.monitor.ID, now.Add(-time.Minute), &endsAt, "activated",
	)
	if err != nil {
		t.Fatal(err)
	}
	interval.CreatedAt = now.Add(-time.Minute)
	if err := fixture.repositories.Maintenance.Create(ctx, port.MaintenanceRecord{Interval: interval, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	worker := newMaintenanceWorker(t, fixture.store, "maintenance-worker", concurrentIDs())
	if count, err := worker.RunOnce(ctx); err != nil || count != 1 {
		t.Fatalf("activation RunOnce() = %d, %v", count, err)
	}
	workerNow, err := fixture.repositories.Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ticks, err := fixture.repositories.StateTicks.ListStateTicks(
		ctx, fixture.monitor.ID, now.Add(-time.Minute), workerNow.Add(time.Minute), 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 1 || ticks[0].ReasonCode != domain.StateTickReasonMaintenance ||
		ticks[0].Lifecycle != domain.MonitorLifecyclePaused || ticks[0].Actor.Kind != domain.StateTickActorSystem ||
		ticks[0].OccurredAt.Before(now) || ticks[0].OccurredAt.After(workerNow) {
		t.Fatalf("activation tick = %#v", ticks)
	}
	if count, err := worker.RunOnce(ctx); err != nil || count != 0 {
		t.Fatalf("idempotent activation RunOnce() = %d, %v", count, err)
	}
}

func TestEndingOverlappingMaintenanceStaysPausedUntilLastEnd(t *testing.T) {
	ctx := context.Background()
	fixture := newProjectionFixture(t, ctx)
	service := NewNotificationAdminService(NotificationAdminServiceConfig{
		Store: fixture.store, Now: func() time.Time { return fixture.now }, NewID: concurrentIDs(),
	})
	first, err := service.CreateMaintenance(ctx, CreateMaintenanceCommand{
		MonitorID: fixture.monitor.ID, StartsAt: fixture.now.Add(-2 * time.Minute), Reason: "first",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateMaintenance(ctx, CreateMaintenanceCommand{
		MonitorID: fixture.monitor.ID, StartsAt: fixture.now.Add(-time.Minute), Reason: "second",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.EndMaintenance(ctx, first.Interval.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.EndMaintenance(ctx, second.Interval.ID); err != nil {
		t.Fatal(err)
	}
	ticks, err := fixture.repositories.StateTicks.ListStateTicks(
		ctx, fixture.monitor.ID, fixture.now.Add(-3*time.Minute), fixture.now.Add(time.Minute), 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks) != 4 {
		t.Fatalf("overlap ticks = %#v", ticks)
	}
	paused, active := 0, 0
	for _, tick := range ticks {
		switch tick.Lifecycle {
		case domain.MonitorLifecyclePaused:
			paused++
		case domain.MonitorLifecycleActive:
			active++
		}
	}
	if paused != 3 || active != 1 {
		t.Fatalf("overlap lifecycle counts paused=%d active=%d ticks=%#v", paused, active, ticks)
	}
}

func TestMaintenanceWorkerExpiryStaysPausedUntilLastOverlapEnds(t *testing.T) {
	ctx := context.Background()
	fixture := newProjectionFixture(t, ctx)
	now, err := fixture.repositories.Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	firstEnd := now.Add(-time.Minute)
	first, err := domain.NewMaintenanceInterval(
		"maintenance-overlap-first", fixture.monitor.ID, now.Add(-2*time.Minute), &firstEnd, "first",
	)
	if err != nil {
		t.Fatal(err)
	}
	secondEnd := now.Add(time.Hour)
	second, err := domain.NewMaintenanceInterval(
		"maintenance-overlap-second", fixture.monitor.ID, now.Add(-time.Minute), &secondEnd, "second",
	)
	if err != nil {
		t.Fatal(err)
	}
	first.CreatedAt, second.CreatedAt = now.Add(-2*time.Minute), now.Add(-time.Minute)
	if err := fixture.repositories.Maintenance.Create(ctx, port.MaintenanceRecord{Interval: first, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.repositories.Maintenance.Create(ctx, port.MaintenanceRecord{Interval: second, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	worker := newMaintenanceWorker(t, fixture.store, "maintenance-worker", concurrentIDs())
	if count, err := worker.RunOnce(ctx); err != nil || count != 1 {
		t.Fatalf("first expiry RunOnce() = %d, %v", count, err)
	}
	if _, err := fixture.repositories.Maintenance.End(ctx, second.ID, now); err != nil {
		t.Fatal(err)
	}
	if count, err := worker.RunOnce(ctx); err != nil || count != 1 {
		t.Fatalf("last expiry RunOnce() = %d, %v", count, err)
	}
	workerNow, err := fixture.repositories.Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ticks, err := fixture.repositories.StateTicks.ListStateTicks(
		ctx, fixture.monitor.ID, now.Add(-3*time.Minute), workerNow.Add(time.Minute), 20,
	)
	if err != nil {
		t.Fatal(err)
	}
	paused, active := 0, 0
	for _, tick := range ticks {
		switch tick.Lifecycle {
		case domain.MonitorLifecyclePaused:
			paused++
		case domain.MonitorLifecycleActive:
			active++
		}
	}
	if paused != 3 || active != 1 {
		t.Fatalf("expiry overlap lifecycle counts paused=%d active=%d ticks=%#v", paused, active, ticks)
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
