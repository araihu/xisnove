package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestProjectionRoutesImmutableSnapshotsAndSuppressesDuringMaintenance(t *testing.T) {
	ctx := context.Background()
	fixture := newProjectionFixture(t, ctx)
	ids := monotonicIDs()

	projectState(t, ctx, fixture, domain.HealthDown, fixture.now, true, ids)
	incident, err := fixture.repositories.Incidents.GetActive(ctx, fixture.monitor.ID)
	if err != nil || incident == nil {
		t.Fatalf("active incident = %#v, %v", incident, err)
	}
	assertTransitionPersistence(t, ctx, fixture, incident.ID, 1, domain.DeliveryPending)
	first := outboxRecords(t, ctx, fixture)[0]
	if first.RenderSnapshot.MonitorID != fixture.monitor.ID ||
		first.RenderSnapshot.MonitorName != fixture.monitor.Name ||
		first.RenderSnapshot.MonitorDescription != fixture.monitor.Description ||
		first.RenderSnapshot.MonitorLabels["environment"] != "homelab" ||
		first.RenderSnapshot.Template != "{{ .MonitorName }} is {{ .State }}" ||
		first.RenderSnapshot.RouteUpdatedAt != fixture.now ||
		first.RenderSnapshot.ChannelKind != domain.NotificationChannelShoutrrr ||
		first.RenderSnapshot.Action != domain.NotificationOpen ||
		first.RenderSnapshot.PreviousState != domain.HealthPending ||
		first.RenderSnapshot.State != domain.HealthDown ||
		first.RenderSnapshot.Severity != domain.IncidentCritical {
		t.Fatalf("first render snapshot = %#v", first.RenderSnapshot)
	}
	if len(first.RenderSnapshot.MonitorLabels) != 1 {
		t.Fatalf("snapshot labels = %#v", first.RenderSnapshot.MonitorLabels)
	}
	encodedSnapshot, err := json.Marshal(first.RenderSnapshot)
	if err != nil || strings.Contains(string(encodedSnapshot), "must-not-enter-snapshot") {
		t.Fatalf("snapshot contains channel secret: %s, %v", encodedSnapshot, err)
	}

	fixture.route.Template = "changed later"
	fixture.route.UpdatedAt = fixture.now.Add(time.Minute)
	if updated, err := fixture.repositories.NotificationRoutes.Update(ctx, fixture.route); err != nil || !updated {
		t.Fatalf("update route = %v, %v", updated, err)
	}
	stored, err := fixture.repositories.NotificationOutbox.Get(ctx, first.ID)
	if err != nil || stored.RenderSnapshot.Template != "{{ .MonitorName }} is {{ .State }}" || stored.RenderSnapshot.RouteUpdatedAt != fixture.now {
		t.Fatalf("immutable snapshot = %#v, %v", stored.RenderSnapshot, err)
	}

	projectState(t, ctx, fixture, domain.HealthDown, fixture.now, true, ids)
	assertTransitionPersistence(t, ctx, fixture, incident.ID, 1, domain.DeliveryPending)

	maintenanceEnd := fixture.now.Add(2 * time.Minute)
	maintenance, err := domain.NewMaintenanceInterval(
		"00000000-0000-4000-8000-000000000030",
		fixture.monitor.ID,
		fixture.now.Add(time.Second),
		&maintenanceEnd,
		"planned change",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.repositories.Maintenance.Create(ctx, port.MaintenanceRecord{
		Interval: maintenance, UpdatedAt: fixture.now,
	}); err != nil {
		t.Fatal(err)
	}
	projectState(t, ctx, fixture, domain.HealthUnknown, fixture.now.Add(time.Minute), true, ids)
	assertTransitionPersistence(t, ctx, fixture, incident.ID, 2, domain.DeliverySuppressed)
	auditEvents, err := fixture.repositories.Audit.ListByIncident(ctx, incident.ID)
	if err != nil {
		t.Fatal(err)
	}
	var suppressedAudit incidentTransitionAuditPayload
	if err := json.Unmarshal(auditEvents[1].Payload, &suppressedAudit); err != nil ||
		!suppressedAudit.Suppressed || suppressedAudit.NotificationCount != 1 {
		t.Fatalf("suppressed audit = %#v, %v", suppressedAudit, err)
	}
	suppressedOutbox := outboxRecords(t, ctx, fixture)[0]
	if suppressedOutbox.SuppressedAt == nil || !suppressedOutbox.SuppressedAt.Equal(fixture.now.Add(time.Minute)) {
		t.Fatalf("suppressed at = %v", suppressedOutbox.SuppressedAt)
	}

	projectState(t, ctx, fixture, domain.HealthUp, fixture.now.Add(3*time.Minute), true, ids)
	assertTransitionPersistence(t, ctx, fixture, incident.ID, 3, domain.DeliveryPending)
	active, err := fixture.repositories.Incidents.GetActive(ctx, fixture.monitor.ID)
	if err != nil || active != nil {
		t.Fatalf("active incident after recovery = %#v, %v", active, err)
	}
}

func TestProjectionRollsBackEveryTransitionWriteFailure(t *testing.T) {
	for _, failure := range []struct {
		name  string
		table string
		event string
	}{
		{name: "monitor health", table: "monitor_health", event: "UPDATE"},
		{name: "incident", table: "incidents", event: "INSERT"},
		{name: "incident event", table: "incident_events", event: "INSERT"},
		{name: "outbox", table: "notification_outbox", event: "INSERT"},
		{name: "audit", table: "audit_events", event: "INSERT"},
	} {
		t.Run(failure.name, func(t *testing.T) {
			ctx := context.Background()
			fixture := newProjectionFixture(t, ctx)
			if _, err := fixture.db.ExecContext(ctx, fmt.Sprintf(`
				CREATE TRIGGER fail_transition AFTER %s ON %s
				BEGIN SELECT RAISE(ABORT, 'injected transition failure'); END
			`, failure.event, failure.table)); err != nil {
				t.Fatal(err)
			}
			err := fixture.store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
				health, err := repositories.Health.GetLocation(ctx, fixture.monitor.ID, fixture.location.ID)
				if err != nil {
					return err
				}
				health.State = domain.HealthDown
				health.LastTransitionAt = fixture.now
				if err := repositories.Health.UpsertLocation(ctx, health); err != nil {
					return err
				}
				return projectAggregateAndIncident(
					ctx, repositories, fixture.monitor.ID, fixture.now, monotonicIDs(), true,
				)
			})
			if err == nil {
				t.Fatal("projection succeeded after injected failure")
			}
			locationHealth, err := fixture.repositories.Health.GetLocation(ctx, fixture.monitor.ID, fixture.location.ID)
			if err != nil || locationHealth.State != domain.HealthPending {
				t.Fatalf("location health after rollback = %#v, %v", locationHealth, err)
			}
			monitorHealth, err := fixture.repositories.Health.GetMonitor(ctx, fixture.monitor.ID)
			if err != nil || monitorHealth.State != domain.HealthPending {
				t.Fatalf("monitor health after rollback = %#v, %v", monitorHealth, err)
			}
			if incident, err := fixture.repositories.Incidents.GetActive(ctx, fixture.monitor.ID); err != nil || incident != nil {
				t.Fatalf("incident after rollback = %#v, %v", incident, err)
			}
			for _, table := range []string{"incident_events", "notification_outbox", "audit_events"} {
				var count int
				if err := fixture.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("%s count after rollback = %d, %v", table, count, err)
				}
			}
		})
	}
}

type projectionFixture struct {
	db interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
		QueryRowContext(context.Context, string, ...any) *sql.Row
	}
	store        port.Store
	repositories port.Repositories
	location     domain.Location
	monitor      domain.Monitor
	route        domain.NotificationRoute
	now          time.Time
}

func newProjectionFixture(t *testing.T, ctx context.Context) projectionFixture {
	t.Helper()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "projection.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(db)
	repositories := store.Repositories()
	now := time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC)
	location, err := domain.NewLocation("00000000-0000-4000-8000-000000000001", "edge", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Locations.Create(ctx, location); err != nil {
		t.Fatal(err)
	}
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID: "00000000-0000-4000-8000-000000000002", Name: "router",
		Description: "homelab router", Labels: map[string]string{"environment": "homelab"},
		Interval: time.Minute, Timeout: 5 * time.Second,
		FailureThreshold: 1, RecoveryThreshold: 1,
		HTTP: domain.HTTPProbe{URL: "https://router.example.com"}, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Monitors.Create(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Monitors.AssignLocation(ctx, port.MonitorLocation{
		MonitorID: monitor.ID, LocationID: location.ID, Required: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Health.UpsertLocation(ctx, domain.LocationHealth{
		MonitorID: monitor.ID, LocationID: location.ID,
		State: domain.HealthPending, LastTransitionAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Health.UpsertMonitor(ctx, domain.MonitorHealth{
		MonitorID: monitor.ID, State: domain.HealthPending, LastTransitionAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	channel, err := domain.NewNotificationChannel(
		"00000000-0000-4000-8000-000000000003", "primary",
		domain.NotificationChannelShoutrrr, true, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.NotificationChannels.Create(ctx, port.NotificationChannelRecord{
		Channel: channel, EncryptedConfig: []byte("must-not-enter-snapshot"), KeyVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	route, err := domain.NewNotificationRoute(domain.NotificationRoute{
		ID: "00000000-0000-4000-8000-000000000004", Name: "homelab",
		ChannelID: channel.ID, LabelMatchers: map[string]string{"environment": "homelab"},
		Actions:    []domain.NotificationAction{domain.NotificationOpen, domain.NotificationChange, domain.NotificationRecover},
		Severities: []domain.IncidentSeverity{domain.IncidentWarning, domain.IncidentCritical},
		Template:   "{{ .MonitorName }} is {{ .State }}", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.NotificationRoutes.Create(ctx, route); err != nil {
		t.Fatal(err)
	}
	unmatched := route.Clone()
	unmatched.ID = "00000000-0000-4000-8000-000000000005"
	unmatched.Name = "production only"
	unmatched.LabelMatchers = map[string]string{"environment": "production"}
	if err := repositories.NotificationRoutes.Create(ctx, unmatched); err != nil {
		t.Fatal(err)
	}
	return projectionFixture{
		db: db, store: store, repositories: repositories,
		location: location, monitor: monitor, route: route, now: now,
	}
}

func projectState(
	t *testing.T,
	ctx context.Context,
	fixture projectionFixture,
	state domain.HealthState,
	at time.Time,
	openUnknown bool,
	newID func() string,
) {
	t.Helper()
	err := fixture.store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		health, err := repositories.Health.GetLocation(ctx, fixture.monitor.ID, fixture.location.ID)
		if err != nil {
			return err
		}
		health.State = state
		health.LastTransitionAt = at
		if err := repositories.Health.UpsertLocation(ctx, health); err != nil {
			return err
		}
		return projectAggregateAndIncident(
			ctx, repositories, fixture.monitor.ID, at, newID, openUnknown,
		)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertTransitionPersistence(
	t *testing.T,
	ctx context.Context,
	fixture projectionFixture,
	incidentID domain.IncidentID,
	want int,
	newestState domain.DeliveryState,
) {
	t.Helper()
	events, err := fixture.repositories.Audit.ListByIncident(ctx, incidentID)
	if err != nil || len(events) != want {
		t.Fatalf("audit events = %#v, %v", events, err)
	}
	outbox := outboxRecords(t, ctx, fixture)
	if len(outbox) != want || outbox[0].State != newestState {
		t.Fatalf("outbox = %#v", outbox)
	}
	var incidentEvents int
	if err := fixture.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM incident_events").Scan(&incidentEvents); err != nil || incidentEvents != want {
		t.Fatalf("incident events = %d, %v", incidentEvents, err)
	}
}

func outboxRecords(t *testing.T, ctx context.Context, fixture projectionFixture) []port.NotificationOutboxRecord {
	t.Helper()
	records, err := fixture.repositories.NotificationOutbox.List(ctx, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func monotonicIDs() func() string {
	value := 100
	return func() string {
		value++
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", value)
	}
}
