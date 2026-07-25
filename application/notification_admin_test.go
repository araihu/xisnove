package application_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestNotificationAdminJourneySealsSecretsAndManagesResources(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/admin.db?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(db)
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	nextID := sequentialUUIDs()
	configuration := application.NewConfigurationService(store, func() time.Time { return now }, nextID)
	location, err := configuration.CreateLocation(ctx, application.CreateLocationCommand{Name: "homelab"})
	if err != nil {
		t.Fatal(err)
	}
	monitor, err := configuration.CreateMonitor(ctx, application.CreateMonitorCommand{
		Name: "router", Description: "edge router", Labels: map[string]string{"site": "home"},
		LocationID: location.ID, RequiredLocation: true, Interval: time.Minute,
		Timeout: 5 * time.Second, FailureThreshold: 2, RecoveryThreshold: 2,
		Probe: domain.ProbeDefinition{Kind: domain.MonitorKindTCP, TCP: domain.TCPProbe{Host: "router.home.arpa", Port: 443}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := xiscrypto.NewEnvelope(
		1, map[uint32][]byte{1: bytes.Repeat([]byte{7}, 32)},
		bytes.NewReader(bytes.Repeat([]byte{3}, 4096)),
	)
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewNotificationAdminService(application.NotificationAdminServiceConfig{
		Store: store, Sealer: sealer, Now: func() time.Time { return now }, NewID: nextID,
	})

	const secretURL = "discord://token@channel"
	channel, err := service.CreateChannel(ctx, application.PutNotificationChannelCommand{
		Name: "on call", Enabled: true,
		Config: application.NotificationChannelConfig{Kind: domain.NotificationChannelShoutrrr, ShoutrrrServiceURL: secretURL},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%#v", channel) == "" || strings.Contains(fmt.Sprintf("%#v", channel), secretURL) {
		t.Fatalf("channel response leaked configuration: %#v", channel)
	}
	record, err := store.Repositories().NotificationChannels.Get(ctx, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(record.EncryptedConfig, []byte(secretURL)) || record.KeyVersion != 1 {
		t.Fatalf("persisted channel was not sealed: %#v", record)
	}
	plaintext, err := sealer.Open(ctx, port.ConfigIdentity{ChannelID: channel.ID, Kind: channel.Kind}, port.SealedConfig{KeyVersion: record.KeyVersion, Ciphertext: record.EncryptedConfig})
	if err != nil || !bytes.Contains(plaintext, []byte(secretURL)) {
		t.Fatalf("open sealed channel = %q, %v", plaintext, err)
	}
	clear(plaintext)

	monitorID := monitor.ID
	route, err := service.CreateRoute(ctx, application.PutNotificationRouteCommand{
		Name: "critical home", ChannelID: channel.ID, MonitorID: &monitorID,
		LabelMatchers: map[string]string{"site": "home"},
		Actions:       []domain.NotificationAction{domain.NotificationOpen, domain.NotificationRecover},
		Severities:    []domain.IncidentSeverity{domain.IncidentCritical},
		Template:      "{{ .MonitorName }} is {{ .State }}", Enabled: true, Precedence: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	listedRoutes, err := service.ListRoutes(ctx, 50, 0)
	if err != nil || len(listedRoutes) != 1 || listedRoutes[0].ID != route.ID {
		t.Fatalf("ListRoutes() = %#v, %v", listedRoutes, err)
	}
	incident := domain.Incident{
		ID: domain.IncidentID(nextID()), MonitorID: monitor.ID, State: domain.HealthDown,
		Severity: domain.IncidentCritical, OpenedAt: now, LastTransitionAt: now,
	}
	event := domain.IncidentEvent{
		ID: nextID(), IncidentID: incident.ID, Action: domain.NotificationOpen,
		PreviousState: domain.HealthPending, State: domain.HealthDown,
		Severity: domain.IncidentCritical, CreatedAt: now,
	}
	deliveryID := domain.NotificationDeliveryID(nextID())
	if err := store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		if err := repositories.Incidents.Open(ctx, incident); err != nil {
			return err
		}
		if err := repositories.Incidents.AppendEvent(ctx, event); err != nil {
			return err
		}
		_, err := repositories.NotificationOutbox.Insert(ctx, port.NotificationOutboxRecord{
			ID: deliveryID, IncidentEventID: event.ID, RouteID: route.ID, ChannelID: channel.ID,
			DedupeKey: "manual-replay", State: domain.DeliveryPermanent,
			AvailableAt: now, CreatedAt: now, UpdatedAt: now,
			RenderSnapshot: domain.RenderSnapshot{
				EventID: event.ID, Action: domain.NotificationOpen, IncidentID: incident.ID,
				MonitorID: monitor.ID, MonitorName: monitor.Name,
				PreviousState: domain.HealthPending, State: domain.HealthDown,
				Severity: domain.IncidentCritical, OccurredAt: now, RouteID: route.ID,
				ChannelID: channel.ID, ChannelKind: channel.Kind, Template: route.Template,
				RouteUpdatedAt: route.UpdatedAt,
			},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.ReplayDelivery(ctx, deliveryID); err != nil {
		t.Fatal(err)
	}
	replayed, err := service.GetDelivery(ctx, deliveryID)
	if err != nil || replayed.Delivery.State != domain.DeliveryPending {
		t.Fatalf("replayed delivery = %#v, %v", replayed, err)
	}
	audit, err := store.Repositories().Audit.ListByIncident(ctx, incident.ID)
	if err != nil || len(audit) != 1 || audit[0].Kind != "notification.delivery-replayed" {
		t.Fatalf("replay audit = %#v, %v", audit, err)
	}
	rollbackID := domain.NotificationDeliveryID(nextID())
	rollbackEvent := event
	rollbackEvent.ID = nextID()
	rollbackEvent.CreatedAt = now.Add(time.Second)
	if err := store.Repositories().Incidents.AppendEvent(ctx, rollbackEvent); err != nil {
		t.Fatal(err)
	}
	replayFixture := replayed.Delivery
	replayFixture.ID = rollbackID
	replayFixture.IncidentEventID = rollbackEvent.ID
	replayFixture.RenderSnapshot.EventID = rollbackEvent.ID
	replayFixture.DedupeKey = "rollback-replay"
	replayFixture.State = domain.DeliveryPermanent
	if inserted, err := store.Repositories().NotificationOutbox.Insert(ctx, replayFixture); err != nil || !inserted {
		t.Fatalf("insert rollback delivery = %v, %v", inserted, err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TRIGGER fail_replay_audit BEFORE INSERT ON audit_events
		BEGIN SELECT RAISE(ABORT, 'injected audit failure'); END
	`); err != nil {
		t.Fatal(err)
	}
	if err := service.ReplayDelivery(ctx, rollbackID); err == nil {
		t.Fatal("ReplayDelivery succeeded after injected audit failure")
	}
	rolledBack, err := service.GetDelivery(ctx, rollbackID)
	if err != nil || rolledBack.Delivery.State != domain.DeliveryPermanent {
		t.Fatalf("replay rollback = %#v, %v", rolledBack, err)
	}
	if err := service.DisableRoute(ctx, route.ID); err != nil {
		t.Fatal(err)
	}
	disabled, err := service.GetRoute(ctx, route.ID)
	if err != nil || disabled.Enabled {
		t.Fatalf("disabled route = %#v, %v", disabled, err)
	}

	start := now.Add(time.Hour)
	end := start.Add(time.Hour)
	maintenance, err := service.CreateMaintenance(ctx, application.CreateMaintenanceCommand{
		MonitorID: monitor.ID, StartsAt: start, EndsAt: &end, Reason: "router upgrade",
	})
	if err != nil {
		t.Fatal(err)
	}
	if maintenance.Interval.CreatedAt != now || maintenance.UpdatedAt != now {
		t.Fatalf("maintenance timestamps = %#v", maintenance)
	}
	if err := service.DeleteMaintenance(ctx, maintenance.Interval.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetMaintenance(ctx, maintenance.Interval.ID); !strings.Contains(fmt.Sprint(err), "not found") {
		t.Fatalf("GetMaintenance(deleted) error = %v", err)
	}

	if err := service.DisableChannel(ctx, channel.ID); err != nil {
		t.Fatal(err)
	}
	disabledChannel, err := service.GetChannel(ctx, channel.ID)
	if err != nil || disabledChannel.Enabled {
		t.Fatalf("disabled channel = %#v, %v", disabledChannel, err)
	}
}

func sequentialUUIDs() func() string {
	var value uint64
	return func() string {
		value++
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", value)
	}
}
