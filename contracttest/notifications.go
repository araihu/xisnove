package contracttest

import (
	"context"
	"errors"
	"testing"
	"time"

	application "github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

const (
	channelID = domain.NotificationChannelID("00000000-0000-4000-8000-000000000020")
	routeID   = domain.NotificationRouteID("00000000-0000-4000-8000-000000000021")
	eventID   = "00000000-0000-4000-8000-000000000022"
	outboxID  = domain.NotificationDeliveryID("00000000-0000-4000-8000-000000000023")
)

func testNotificationPersistence(t *testing.T, store application.Store) {
	t.Helper()
	ctx := context.Background()
	fixture := seedWithoutRun(t, store, 1)
	channel, err := domain.NewNotificationChannel(
		channelID, "primary", domain.NotificationChannelShoutrrr, true, fixture.now,
	)
	if err != nil {
		t.Fatal(err)
	}
	channelRecord := application.NotificationChannelRecord{
		Channel: channel, EncryptedConfig: []byte("ciphertext"), KeyVersion: 7,
	}
	if err := store.Repositories().NotificationChannels.Create(ctx, channelRecord); err != nil {
		t.Fatal(err)
	}
	gotChannel, err := store.Repositories().NotificationChannels.Get(ctx, channelID)
	if err != nil {
		t.Fatal(err)
	}
	if gotChannel.Channel != channel || string(gotChannel.EncryptedConfig) != "ciphertext" || gotChannel.KeyVersion != 7 {
		t.Fatalf("channel = %#v", gotChannel)
	}
	versions, err := store.Repositories().NotificationChannels.ListKeyVersions(ctx)
	if err != nil || len(versions) != 1 || versions[0] != 7 {
		t.Fatalf("key versions = %v, %v", versions, err)
	}

	target := fixture.monitor.ID
	route, err := domain.NewNotificationRoute(domain.NotificationRoute{
		ID: routeID, Name: "critical", ChannelID: channelID, MonitorID: &target,
		LabelMatchers: map[string]string{"environment": "homelab"},
		Actions:       []domain.NotificationAction{domain.NotificationOpen},
		Severities:    []domain.IncidentSeverity{domain.IncidentCritical},
		Template:      "down", Enabled: true, Precedence: 10,
		CreatedAt: fixture.now, UpdatedAt: fixture.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Repositories().NotificationRoutes.Create(ctx, route); err != nil {
		t.Fatal(err)
	}
	gotRoute, err := store.Repositories().NotificationRoutes.Get(ctx, routeID)
	if err != nil {
		t.Fatal(err)
	}
	if !gotRoute.Matches(domain.NotificationEvent{
		Action:    domain.NotificationOpen,
		Event:     domain.IncidentEvent{Severity: domain.IncidentCritical},
		MonitorID: fixture.monitor.ID,
		Labels:    map[string]string{"environment": "homelab"},
	}) {
		t.Fatalf("route = %#v", gotRoute)
	}

	incident := domain.Incident{
		ID: "00000000-0000-4000-8000-000000000024", MonitorID: fixture.monitor.ID,
		State: domain.HealthDown, Severity: domain.IncidentCritical,
		OpenedAt: fixture.now, LastTransitionAt: fixture.now,
	}
	if err := store.Repositories().Incidents.Open(ctx, incident); err != nil {
		t.Fatal(err)
	}
	if err := store.Repositories().Incidents.AppendEvent(ctx, domain.IncidentEvent{
		ID: eventID, IncidentID: incident.ID, Action: domain.NotificationOpen,
		State: domain.HealthDown, Severity: domain.IncidentCritical, CreatedAt: fixture.now,
	}); err != nil {
		t.Fatal(err)
	}
	identity, err := domain.NewNotificationIdentity(eventID, routeID, channelID)
	if err != nil {
		t.Fatal(err)
	}
	record := application.NotificationOutboxRecord{
		ID: outboxID, IncidentEventID: eventID, RouteID: routeID, ChannelID: channelID,
		DedupeKey: identity,
		RenderSnapshot: domain.RenderSnapshot{
			EventID: eventID, Action: domain.NotificationOpen, IncidentID: incident.ID,
			MonitorID: fixture.monitor.ID, MonitorName: fixture.monitor.Name,
			MonitorLabels: map[string]string{"environment": "homelab"},
			State:         domain.HealthDown, Severity: domain.IncidentCritical,
			OccurredAt: fixture.now, RouteID: routeID, ChannelID: channelID,
			ChannelKind: channel.Kind, Template: route.Template,
		},
		State: domain.DeliveryPending, AvailableAt: fixture.now,
		CreatedAt: fixture.now, UpdatedAt: fixture.now,
	}
	inserted, err := store.Repositories().NotificationOutbox.Insert(ctx, record)
	if err != nil || !inserted {
		t.Fatalf("Insert() = %v, %v", inserted, err)
	}
	duplicate := record
	duplicate.ID = "00000000-0000-4000-8000-000000000025"
	inserted, err = store.Repositories().NotificationOutbox.Insert(ctx, duplicate)
	if err != nil || inserted {
		t.Fatalf("duplicate Insert() = %v, %v", inserted, err)
	}
	record.RenderSnapshot.MonitorLabels["environment"] = "mutated"
	stored, err := store.Repositories().NotificationOutbox.Get(ctx, outboxID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RenderSnapshot.MonitorLabels["environment"] != "homelab" {
		t.Fatalf("snapshot = %#v", stored.RenderSnapshot)
	}

	claimOne := application.ClaimNotificationParams{
		Owner: "worker-1", ClaimTokenHash: []byte("claim-one"),
		ClaimExpiresAt: fixture.now.Add(30 * time.Second), Now: fixture.now,
	}
	claimed, err := store.Repositories().NotificationOutbox.ClaimDue(ctx, claimOne)
	if err != nil || claimed.ID != outboxID || claimed.State != domain.DeliveryClaimed {
		t.Fatalf("ClaimDue() = %#v, %v", claimed, err)
	}
	if _, err := store.Repositories().NotificationOutbox.ClaimDue(ctx, claimOne); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("second ClaimDue() error = %v", err)
	}
	wrong, err := store.Repositories().NotificationOutbox.MarkRetrying(ctx, application.FinalizeNotificationParams{
		ID: outboxID, ClaimTokenHash: []byte("wrong"), At: fixture.now,
		AvailableAt: fixture.now.Add(time.Minute), ErrorClass: "timeout", Diagnostic: "bounded",
	})
	if err != nil || wrong {
		t.Fatalf("wrong-token MarkRetrying() = %v, %v", wrong, err)
	}
	appendAttempt(t, store, fixture.now, 1, application.NotificationAttemptTransientFailure)
	retried, err := store.Repositories().NotificationOutbox.MarkRetrying(ctx, application.FinalizeNotificationParams{
		ID: outboxID, ClaimTokenHash: claimOne.ClaimTokenHash, At: fixture.now,
		AvailableAt: fixture.now.Add(time.Minute), ErrorClass: "timeout", Diagnostic: "bounded",
	})
	if err != nil || !retried {
		t.Fatalf("MarkRetrying() = %v, %v", retried, err)
	}

	claimTwo := application.ClaimNotificationParams{
		Owner: "worker-2", ClaimTokenHash: []byte("claim-two"),
		ClaimExpiresAt: fixture.now.Add(2 * time.Minute), Now: fixture.now.Add(time.Minute),
	}
	if _, err := store.Repositories().NotificationOutbox.ClaimDue(ctx, claimTwo); err != nil {
		t.Fatal(err)
	}
	appendAttempt(t, store, fixture.now.Add(time.Minute), 2, application.NotificationAttemptPermanentFailure)
	permanent, err := store.Repositories().NotificationOutbox.MarkPermanentFailure(ctx, application.FinalizeNotificationParams{
		ID: outboxID, ClaimTokenHash: claimTwo.ClaimTokenHash, At: fixture.now.Add(time.Minute),
		ErrorClass: "invalid-config", Diagnostic: "bounded",
	})
	if err != nil || !permanent {
		t.Fatalf("MarkPermanentFailure() = %v, %v", permanent, err)
	}
	replayed, err := store.Repositories().NotificationOutbox.Replay(ctx, outboxID, fixture.now.Add(2*time.Minute))
	if err != nil || !replayed {
		t.Fatalf("Replay() = %v, %v", replayed, err)
	}

	claimThree := application.ClaimNotificationParams{
		Owner: "worker-3", ClaimTokenHash: []byte("claim-three"),
		ClaimExpiresAt: fixture.now.Add(3 * time.Minute), Now: fixture.now.Add(2 * time.Minute),
	}
	if _, err := store.Repositories().NotificationOutbox.ClaimDue(ctx, claimThree); err != nil {
		t.Fatal(err)
	}
	appendAttempt(t, store, fixture.now.Add(2*time.Minute), 3, application.NotificationAttemptDelivered)
	delivered, err := store.Repositories().NotificationOutbox.MarkDelivered(ctx, application.FinalizeNotificationParams{
		ID: outboxID, ClaimTokenHash: claimThree.ClaimTokenHash, At: fixture.now.Add(2 * time.Minute),
	})
	if err != nil || !delivered {
		t.Fatalf("MarkDelivered() = %v, %v", delivered, err)
	}
	stored, err = store.Repositories().NotificationOutbox.Get(ctx, outboxID)
	if err != nil || stored.State != domain.DeliveryDelivered || stored.AttemptCount != 3 {
		t.Fatalf("delivered record = %#v, %v", stored, err)
	}
	attempts, err := store.Repositories().NotificationOutbox.ListAttempts(ctx, outboxID)
	if err != nil || len(attempts) != 3 {
		t.Fatalf("attempts = %#v, %v", attempts, err)
	}
}

func appendAttempt(
	t *testing.T,
	store application.Store,
	at time.Time,
	ordinal uint32,
	outcome application.NotificationAttemptOutcome,
) {
	t.Helper()
	err := store.Repositories().NotificationOutbox.AppendAttempt(context.Background(), application.NotificationDeliveryAttemptRecord{
		ID:       "00000000-0000-4000-8002-" + leftPadOrdinal(ordinal),
		OutboxID: outboxID, Ordinal: ordinal, StartedAt: at,
		FinishedAt: at.Add(time.Second), Outcome: outcome,
		ErrorClass: "class", Diagnostic: "bounded", ProviderReceipt: "receipt",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func leftPadOrdinal(ordinal uint32) string {
	switch ordinal {
	case 1:
		return "000000000001"
	case 2:
		return "000000000002"
	default:
		return "000000000003"
	}
}

func testMaintenanceAndOperationLeases(t *testing.T, store application.Store) {
	t.Helper()
	ctx := context.Background()
	fixture := seedWithoutRun(t, store, 1)
	end := fixture.now.Add(-time.Minute)
	interval, err := domain.NewMaintenanceInterval(
		"00000000-0000-4000-8000-000000000030", fixture.monitor.ID,
		fixture.now.Add(-time.Hour), &end, "upgrade",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Repositories().Maintenance.Create(ctx, application.MaintenanceRecord{
		Interval: interval, UpdatedAt: fixture.now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	claim := application.ClaimMaintenanceParams{
		Owner: "worker", ClaimTokenHash: []byte("maintenance-claim"),
		ClaimExpiresAt: fixture.now.Add(time.Minute), Now: fixture.now,
	}
	claimed, err := store.Repositories().Maintenance.ClaimEnded(ctx, claim)
	if err != nil || claimed.Interval.ID != interval.ID {
		t.Fatalf("ClaimEnded() = %#v, %v", claimed, err)
	}
	if _, err := store.Repositories().Maintenance.ClaimEnded(ctx, claim); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("second ClaimEnded() error = %v", err)
	}
	processed, err := store.Repositories().Maintenance.MarkEndedProcessed(
		ctx, interval.ID, claim.ClaimTokenHash, fixture.now,
	)
	if err != nil || !processed {
		t.Fatalf("MarkEndedProcessed() = %v, %v", processed, err)
	}

	lease := application.OperationLeaseRecord{
		Key: "retention", Owner: "worker-1", TokenHash: []byte("lease-one"),
		ExpiresAt: fixture.now.Add(time.Minute), Cursor: []byte(`{"day":"2026-07-24"}`),
		UpdatedAt: fixture.now,
	}
	gotLease, err := store.Repositories().Retention.ClaimLease(ctx, lease, fixture.now)
	if err != nil || gotLease.Key != lease.Key {
		t.Fatalf("ClaimLease() = %#v, %v", gotLease, err)
	}
	if _, err := store.Repositories().Retention.ClaimLease(ctx, lease, fixture.now); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("second ClaimLease() error = %v", err)
	}
	replacement := lease
	replacement.Owner = "worker-2"
	replacement.TokenHash = []byte("lease-two")
	replacement.ExpiresAt = fixture.now.Add(3 * time.Minute)
	replacement.UpdatedAt = fixture.now.Add(2 * time.Minute)
	if _, err := store.Repositories().Retention.ClaimLease(ctx, replacement, replacement.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	released, err := store.Repositories().Retention.ReleaseLease(ctx, replacement.Key, replacement.TokenHash)
	if err != nil || !released {
		t.Fatalf("ReleaseLease() = %v, %v", released, err)
	}
}
