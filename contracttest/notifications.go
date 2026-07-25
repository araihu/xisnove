package contracttest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
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

func testNotificationPersistence(t *testing.T, store application.UnitOfWork) {
	t.Helper()
	ctx := context.Background()
	var duplicateAttempt application.NotificationDeliveryAttemptRecord
	transact(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		fixture := seedWithoutRun(t, ctx, repositories, 1)
		channel, err := domain.NewNotificationChannel(
			channelID, "primary", domain.NotificationChannelShoutrrr, true, fixture.now,
		)
		if err != nil {
			t.Fatal(err)
		}
		channelRecord := application.NotificationChannelRecord{
			Channel: channel, EncryptedConfig: []byte("ciphertext"), KeyVersion: 7,
		}
		if err := repositories.NotificationChannels.Create(ctx, channelRecord); err != nil {
			t.Fatal(err)
		}
		gotChannel, err := repositories.NotificationChannels.Get(ctx, channelID)
		if err != nil {
			t.Fatal(err)
		}
		if gotChannel.Channel != channel || string(gotChannel.EncryptedConfig) != "ciphertext" || gotChannel.KeyVersion != 7 {
			t.Fatalf("channel = %#v", gotChannel)
		}
		secondChannel, err := domain.NewNotificationChannel(
			"00000000-0000-4000-8000-000000000028",
			"secondary",
			domain.NotificationChannelAlertmanager,
			true,
			fixture.now,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := repositories.NotificationChannels.Create(ctx, application.NotificationChannelRecord{
			Channel: secondChannel, EncryptedConfig: []byte("other-ciphertext"), KeyVersion: 9,
		}); err != nil {
			t.Fatal(err)
		}
		versions, err := repositories.NotificationChannels.ListKeyVersions(ctx)
		if err != nil || len(versions) != 2 || versions[0] != 7 || versions[1] != 9 {
			t.Fatalf("key versions = %v, %v", versions, err)
		}
		needsRotation, err := repositories.NotificationChannels.ListNeedingKeyVersion(ctx, 9, 1)
		if err != nil || len(needsRotation) != 1 || needsRotation[0].Channel.ID != channelID {
			t.Fatalf("channels needing version 9 = %#v, %v", needsRotation, err)
		}
		needsRotation, err = repositories.NotificationChannels.ListNeedingKeyVersion(ctx, 7, 1)
		if err != nil || len(needsRotation) != 1 || needsRotation[0].Channel.ID != secondChannel.ID {
			t.Fatalf("channels needing version 7 = %#v, %v", needsRotation, err)
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
		if err := repositories.NotificationRoutes.Create(ctx, route); err != nil {
			t.Fatal(err)
		}
		gotRoute, err := repositories.NotificationRoutes.Get(ctx, routeID)
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
		if err := repositories.Incidents.Open(ctx, incident); err != nil {
			t.Fatal(err)
		}
		if err := repositories.Incidents.AppendEvent(ctx, domain.IncidentEvent{
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
		inserted, err := repositories.NotificationOutbox.Insert(ctx, record)
		if err != nil || !inserted {
			t.Fatalf("Insert() = %v, %v", inserted, err)
		}
		duplicate := record
		duplicate.ID = "00000000-0000-4000-8000-000000000025"
		inserted, err = repositories.NotificationOutbox.Insert(ctx, duplicate)
		if err != nil || inserted {
			t.Fatalf("duplicate Insert() = %v, %v", inserted, err)
		}
		record.RenderSnapshot.MonitorLabels["environment"] = "mutated"
		stored, err := repositories.NotificationOutbox.Get(ctx, outboxID)
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
		claimed, err := repositories.NotificationOutbox.ClaimDue(ctx, claimOne)
		if err != nil || claimed.ID != outboxID || claimed.State != domain.DeliveryClaimed {
			t.Fatalf("ClaimDue() = %#v, %v", claimed, err)
		}
		if _, err := repositories.NotificationOutbox.ClaimDue(ctx, claimOne); !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("second ClaimDue() error = %v", err)
		}
		wrong, err := repositories.NotificationOutbox.MarkRetrying(ctx, application.FinalizeNotificationParams{
			ID: outboxID, ClaimTokenHash: []byte("wrong"), At: fixture.now,
			AvailableAt: fixture.now.Add(time.Minute), ErrorClass: "timeout", Diagnostic: "bounded",
		})
		if err != nil || wrong {
			t.Fatalf("wrong-token MarkRetrying() = %v, %v", wrong, err)
		}
		appendAttempt(t, ctx, repositories, fixture.now, 1, application.NotificationAttemptTransientFailure)
		retried, err := repositories.NotificationOutbox.MarkRetrying(ctx, application.FinalizeNotificationParams{
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
		if _, err := repositories.NotificationOutbox.ClaimDue(ctx, claimTwo); err != nil {
			t.Fatal(err)
		}
		appendAttempt(t, ctx, repositories, fixture.now.Add(time.Minute), 2, application.NotificationAttemptPermanentFailure)
		permanent, err := repositories.NotificationOutbox.MarkPermanentFailure(ctx, application.FinalizeNotificationParams{
			ID: outboxID, ClaimTokenHash: claimTwo.ClaimTokenHash, At: fixture.now.Add(time.Minute),
			ErrorClass: "invalid-config", Diagnostic: "bounded",
		})
		if err != nil || !permanent {
			t.Fatalf("MarkPermanentFailure() = %v, %v", permanent, err)
		}
		replayed, err := repositories.NotificationOutbox.Replay(ctx, outboxID, fixture.now.Add(2*time.Minute))
		if err != nil || !replayed {
			t.Fatalf("Replay() = %v, %v", replayed, err)
		}

		claimThree := application.ClaimNotificationParams{
			Owner: "worker-3", ClaimTokenHash: []byte("claim-three"),
			ClaimExpiresAt: fixture.now.Add(3 * time.Minute), Now: fixture.now.Add(2 * time.Minute),
		}
		if _, err := repositories.NotificationOutbox.ClaimDue(ctx, claimThree); err != nil {
			t.Fatal(err)
		}
		appendAttempt(t, ctx, repositories, fixture.now.Add(2*time.Minute), 3, application.NotificationAttemptDelivered)
		delivered, err := repositories.NotificationOutbox.MarkDelivered(ctx, application.FinalizeNotificationParams{
			ID: outboxID, ClaimTokenHash: claimThree.ClaimTokenHash, At: fixture.now.Add(2 * time.Minute),
		})
		if err != nil || !delivered {
			t.Fatalf("MarkDelivered() = %v, %v", delivered, err)
		}
		stored, err = repositories.NotificationOutbox.Get(ctx, outboxID)
		if err != nil || stored.State != domain.DeliveryDelivered || stored.AttemptCount != 3 {
			t.Fatalf("delivered record = %#v, %v", stored, err)
		}
		attempts, err := repositories.NotificationOutbox.ListAttempts(ctx, outboxID)
		if err != nil || len(attempts) != 3 {
			t.Fatalf("attempts = %#v, %v", attempts, err)
		}
		duplicateAttempt = attempts[2]
		duplicateAttempt.ID = "00000000-0000-4000-8002-000000000099"

		suppressedEventID := "00000000-0000-4000-8000-000000000026"
		if err := repositories.Incidents.AppendEvent(ctx, domain.IncidentEvent{
			ID: suppressedEventID, IncidentID: incident.ID, Action: domain.NotificationChange,
			State: domain.HealthDown, Severity: domain.IncidentCritical,
			CreatedAt: fixture.now.Add(3 * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
		suppressedID := domain.NotificationDeliveryID("00000000-0000-4000-8000-000000000027")
		suppressed := record
		suppressed.ID = suppressedID
		suppressed.IncidentEventID = suppressedEventID
		suppressed.DedupeKey = suppressedEventID + ":" + string(routeID) + ":" + string(channelID)
		suppressed.RenderSnapshot.EventID = suppressedEventID
		suppressed.RenderSnapshot.Action = domain.NotificationChange
		suppressed.AvailableAt = fixture.now.Add(3 * time.Minute)
		suppressed.CreatedAt = suppressed.AvailableAt
		suppressed.UpdatedAt = suppressed.AvailableAt
		if inserted, err := repositories.NotificationOutbox.Insert(ctx, suppressed); err != nil || !inserted {
			t.Fatalf("insert suppressed candidate = %v, %v", inserted, err)
		}
		suppressionClaim := application.ClaimNotificationParams{
			Owner: "worker-4", ClaimTokenHash: []byte("claim-four"),
			ClaimExpiresAt: fixture.now.Add(4 * time.Minute), Now: fixture.now.Add(3 * time.Minute),
		}
		if _, err := repositories.NotificationOutbox.ClaimDue(ctx, suppressionClaim); err != nil {
			t.Fatal(err)
		}
		marked, err := repositories.NotificationOutbox.MarkSuppressed(ctx, application.FinalizeNotificationParams{
			ID: suppressedID, ClaimTokenHash: suppressionClaim.ClaimTokenHash,
			At: fixture.now.Add(3 * time.Minute),
		})
		if err != nil || !marked {
			t.Fatalf("MarkSuppressed() = %v, %v", marked, err)
		}
		storedSuppressed, err := repositories.NotificationOutbox.Get(ctx, suppressedID)
		if err != nil || storedSuppressed.State != domain.DeliverySuppressed || storedSuppressed.SuppressedAt == nil {
			t.Fatalf("suppressed record = %#v, %v", storedSuppressed, err)
		}

		return nil
	})

	err := store.Transact(ctx, func(
		ctx context.Context,
		repositories application.Repositories,
	) error {
		return repositories.NotificationOutbox.AppendAttempt(ctx, duplicateAttempt)
	})
	if err == nil {
		t.Fatal("duplicate attempt ordinal succeeded")
	}
}

func testNotificationOrderingAndCompetingClaims(t *testing.T, store application.UnitOfWork) {
	t.Helper()
	ctx := context.Background()
	var fixture seeded
	transact(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		fixture = seedWithoutRun(t, ctx, repositories, 1)
		channel, err := domain.NewNotificationChannel(
			channelID, "ordered", domain.NotificationChannelShoutrrr, true, fixture.now,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := repositories.NotificationChannels.Create(ctx, application.NotificationChannelRecord{
			Channel: channel, EncryptedConfig: []byte("ciphertext"), KeyVersion: 1,
		}); err != nil {
			t.Fatal(err)
		}

		routes := []domain.NotificationRoute{
			{ID: "00000000-0000-4000-8000-000000000043", Name: "third", Precedence: 20},
			{ID: "00000000-0000-4000-8000-000000000042", Name: "second", Precedence: 10},
			{ID: "00000000-0000-4000-8000-000000000041", Name: "first", Precedence: 10},
		}
		for index := range routes {
			routes[index].ChannelID = channelID
			routes[index].Actions = []domain.NotificationAction{domain.NotificationOpen}
			routes[index].Severities = []domain.IncidentSeverity{domain.IncidentCritical}
			routes[index].Template = "ordered"
			routes[index].Enabled = true
			routes[index].CreatedAt = fixture.now
			routes[index].UpdatedAt = fixture.now
			route, err := domain.NewNotificationRoute(routes[index])
			if err != nil {
				t.Fatal(err)
			}
			if err := repositories.NotificationRoutes.Create(ctx, route); err != nil {
				t.Fatal(err)
			}
		}
		ordered, err := repositories.NotificationRoutes.ListEnabled(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(ordered) != 3 || ordered[0].Name != "first" || ordered[1].Name != "second" || ordered[2].Name != "third" {
			t.Fatalf("route order = %#v", ordered)
		}

		incident := domain.Incident{
			ID: "00000000-0000-4000-8000-000000000044", MonitorID: fixture.monitor.ID,
			State: domain.HealthDown, Severity: domain.IncidentCritical,
			OpenedAt: fixture.now, LastTransitionAt: fixture.now,
		}
		if err := repositories.Incidents.Open(ctx, incident); err != nil {
			t.Fatal(err)
		}
		claimEventID := "00000000-0000-4000-8000-000000000045"
		if err := repositories.Incidents.AppendEvent(ctx, domain.IncidentEvent{
			ID: claimEventID, IncidentID: incident.ID, Action: domain.NotificationOpen,
			State: domain.HealthDown, Severity: domain.IncidentCritical, CreatedAt: fixture.now,
		}); err != nil {
			t.Fatal(err)
		}
		claimOutboxID := domain.NotificationDeliveryID("00000000-0000-4000-8000-000000000046")
		inserted, err := repositories.NotificationOutbox.Insert(ctx, application.NotificationOutboxRecord{
			ID: claimOutboxID, IncidentEventID: claimEventID, RouteID: routes[0].ID,
			ChannelID: channelID, DedupeKey: "competing-claim",
			RenderSnapshot: domain.RenderSnapshot{
				EventID: claimEventID, Action: domain.NotificationOpen, IncidentID: incident.ID,
				MonitorID: fixture.monitor.ID, MonitorName: fixture.monitor.Name,
				State: domain.HealthDown, Severity: domain.IncidentCritical,
				OccurredAt: fixture.now, RouteID: routes[0].ID, ChannelID: channelID,
				ChannelKind: channel.Kind, Template: "ordered",
			},
			State: domain.DeliveryPending, AvailableAt: fixture.now,
			CreatedAt: fixture.now, UpdatedAt: fixture.now,
		})
		if err != nil || !inserted {
			t.Fatalf("insert competing outbox = %v, %v", inserted, err)
		}

		return nil
	})

	start := make(chan struct{})
	errs := make([]error, 2)
	var group sync.WaitGroup
	for index := range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			err := store.Transact(ctx, func(
				ctx context.Context,
				repositories application.Repositories,
			) error {
				_, errs[index] = repositories.NotificationOutbox.ClaimDue(
					ctx,
					application.ClaimNotificationParams{
						Owner: "worker", ClaimTokenHash: []byte{byte(index + 1)},
						ClaimExpiresAt: fixture.now.Add(time.Minute), Now: fixture.now,
					},
				)
				return nil
			})
			if err != nil {
				errs[index] = err
			}
		}()
	}
	close(start)
	group.Wait()
	winners := 0
	for _, claimErr := range errs {
		if claimErr == nil {
			winners++
			continue
		}
		if !errors.Is(claimErr, application.ErrNotFound) {
			t.Fatalf("competing ClaimDue() error = %v", claimErr)
		}
	}
	if winners != 1 {
		t.Fatalf("claim winners = %d, errors = %v", winners, errs)
	}

	rollbackID := domain.NotificationChannelID("00000000-0000-4000-8000-000000000047")
	stop := errors.New("rollback notification")
	err := store.Transact(ctx, func(
		ctx context.Context,
		repositories application.Repositories,
	) error {
		rollbackChannel, err := domain.NewNotificationChannel(
			rollbackID, "rollback", domain.NotificationChannelShoutrrr, true, fixture.now,
		)
		if err != nil {
			return err
		}
		if err := repositories.NotificationChannels.Create(ctx, application.NotificationChannelRecord{
			Channel: rollbackChannel, EncryptedConfig: []byte("ciphertext"), KeyVersion: 1,
		}); err != nil {
			return err
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("notification rollback = %v", err)
	}
	view(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		_, err := repositories.NotificationChannels.Get(ctx, rollbackID)
		if !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("rolled-back channel Get() = %v", err)
		}
		return nil
	})
}

func appendAttempt(
	t *testing.T,
	ctx context.Context,
	repositories application.Repositories,
	at time.Time,
	ordinal uint32,
	outcome application.NotificationAttemptOutcome,
) {
	t.Helper()
	err := repositories.NotificationOutbox.AppendAttempt(ctx, application.NotificationDeliveryAttemptRecord{
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

func testMaintenanceAndOperationLeases(t *testing.T, store application.UnitOfWork) {
	t.Helper()
	ctx := context.Background()
	transact(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		fixture := seedWithoutRun(t, ctx, repositories, 1)
		end := fixture.now.Add(-time.Minute)
		interval, err := domain.NewMaintenanceInterval(
			"00000000-0000-4000-8000-000000000030", fixture.monitor.ID,
			fixture.now.Add(-time.Hour), &end, "upgrade",
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := repositories.Maintenance.Create(ctx, application.MaintenanceRecord{
			Interval: interval, UpdatedAt: fixture.now.Add(-time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
		claim := application.ClaimMaintenanceParams{
			Owner: "worker", ClaimTokenHash: []byte("maintenance-claim"),
			ClaimExpiresAt: fixture.now.Add(time.Minute), Now: fixture.now,
		}
		claimed, err := repositories.Maintenance.ClaimEnded(ctx, claim)
		if err != nil || claimed.Interval.ID != interval.ID {
			t.Fatalf("ClaimEnded() = %#v, %v", claimed, err)
		}
		if _, err := repositories.Maintenance.ClaimEnded(ctx, claim); !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("second ClaimEnded() error = %v", err)
		}
		processed, err := repositories.Maintenance.MarkEndedProcessed(
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
		gotLease, err := repositories.Retention.ClaimLease(ctx, lease, fixture.now)
		if err != nil || gotLease.Key != lease.Key {
			t.Fatalf("ClaimLease() = %#v, %v", gotLease, err)
		}
		if _, err := repositories.Retention.ClaimLease(ctx, lease, fixture.now); !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("second ClaimLease() error = %v", err)
		}
		replacement := lease
		replacement.Owner = "worker-2"
		replacement.TokenHash = []byte("lease-two")
		replacement.ExpiresAt = fixture.now.Add(3 * time.Minute)
		replacement.UpdatedAt = fixture.now.Add(2 * time.Minute)
		if _, err := repositories.Retention.ClaimLease(ctx, replacement, replacement.UpdatedAt); err != nil {
			t.Fatal(err)
		}
		released, err := repositories.Retention.ReleaseLease(ctx, replacement.Key, replacement.TokenHash)
		if err != nil || !released {
			t.Fatalf("ReleaseLease() = %v, %v", released, err)
		}

		return nil
	})
}

func testAuditAndBoundedDailyRetention(t *testing.T, store application.UnitOfWork) {
	t.Helper()
	ctx := context.Background()
	transact(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		fixture := seedWithoutRun(t, ctx, repositories, 0)
		incidentID := domain.IncidentID("00000000-0000-4000-8000-000000000050")
		if err := repositories.Incidents.Open(ctx, domain.Incident{
			ID: incidentID, MonitorID: fixture.monitor.ID, State: domain.HealthDown,
			Severity: domain.IncidentCritical, OpenedAt: fixture.now,
			LastTransitionAt: fixture.now,
		}); err != nil {
			t.Fatal(err)
		}
		event := application.AuditEventRecord{
			ID: "00000000-0000-4000-8000-000000000051", Kind: "incident.opened",
			SubjectKind: "monitor", SubjectID: string(fixture.monitor.ID), IncidentID: &incidentID,
			Payload: []byte(`{"state":"down"}`), CreatedAt: fixture.now,
		}
		if err := repositories.Audit.Append(ctx, event); err != nil {
			t.Fatal(err)
		}
		events, err := repositories.Audit.ListByIncident(ctx, incidentID)
		var payload map[string]string
		if err != nil || len(events) != 1 || json.Unmarshal(events[0].Payload, &payload) != nil || payload["state"] != "down" {
			t.Fatalf("audit events = %#v, %v", events, err)
		}
		event.Payload[0] = 'x'
		if string(events[0].Payload) == string(event.Payload) {
			t.Fatal("audit payload aliases caller memory")
		}

		for day, passing := range []uint64{1, 2} {
			record := application.DailyUptimeRecord{
				MonitorID: fixture.monitor.ID, Day: fixture.now.AddDate(0, 0, day-2),
				Passing: passing, Failing: 1, Unknown: 1, Observed: time.Minute,
				UpdatedAt: fixture.now,
			}
			if err := repositories.Retention.UpsertDailyUptime(ctx, record); err != nil {
				t.Fatal(err)
			}
		}
		updated := application.DailyUptimeRecord{
			MonitorID: fixture.monitor.ID, Day: fixture.now.AddDate(0, 0, -1),
			Passing: 9, Failing: 2, Unknown: 0, Observed: 2 * time.Minute,
			UpdatedAt: fixture.now.Add(time.Minute),
		}
		if err := repositories.Retention.UpsertDailyUptime(ctx, updated); err != nil {
			t.Fatal(err)
		}
		daily, err := repositories.Retention.ListDailyUptime(
			ctx, fixture.monitor.ID, fixture.now.AddDate(0, 0, -2), fixture.now.AddDate(0, 0, 1),
		)
		if err != nil || len(daily) != 2 || daily[1].Passing != 9 {
			t.Fatalf("daily uptime = %#v, %v", daily, err)
		}
		deleted, err := repositories.Retention.DeleteExpiredDailyUptime(ctx, fixture.now, 1)
		if err != nil || deleted != 1 {
			t.Fatalf("bounded DeleteExpiredDailyUptime() = %d, %v", deleted, err)
		}
		daily, err = repositories.Retention.ListDailyUptime(
			ctx, fixture.monitor.ID, fixture.now.AddDate(0, 0, -2), fixture.now.AddDate(0, 0, 1),
		)
		if err != nil || len(daily) != 1 {
			t.Fatalf("daily uptime after bounded delete = %#v, %v", daily, err)
		}

		return nil
	})
}

func testAggregationCursorAndBoundedRawRetention(t *testing.T, store application.UnitOfWork) {
	t.Helper()
	ctx := context.Background()
	transact(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		fixture := seedWithoutRun(t, ctx, repositories, 1)
		cutoff := fixture.now
		resultIDs := make([]string, 0, 3)
		for index, receivedAt := range []time.Time{cutoff.Add(-time.Second), cutoff, cutoff.Add(time.Second)} {
			runID := domain.CheckRunID(fmt.Sprintf("00000000-0000-4000-8002-%012d", index+1))
			inserted, err := repositories.Runs.Insert(ctx, application.NewRunRecord{
				ID: runID, MonitorID: fixture.monitor.ID, LocationID: fixture.location.ID,
				ScheduledFor: receivedAt.Add(-time.Second), Probe: fixture.monitor.Probe(), Timeout: fixture.monitor.Timeout,
			})
			if err != nil || !inserted {
				t.Fatalf("aggregation run %d = %v, %v", index, inserted, err)
			}
			resultID := fmt.Sprintf("00000000-0000-4000-8003-%012d", index+1)
			resultIDs = append(resultIDs, resultID)
			inserted, err = repositories.Results.Insert(ctx, application.ProbeResultRecord{
				ID: resultID, RunID: runID, AgentID: fixture.agentIDs[0],
				StartedAt: receivedAt.Add(-time.Millisecond), FinishedAt: receivedAt,
				ReceivedAt: receivedAt, Passed: index != 0, Latency: time.Millisecond,
			})
			if err != nil || !inserted {
				t.Fatalf("aggregation result %d = %v, %v", index, inserted, err)
			}
		}
		page, err := repositories.Retention.ListAggregationResults(
			ctx, cutoff.Add(-time.Minute), cutoff.Add(time.Minute), cutoff.Add(-time.Minute), "", 2,
		)
		if err != nil || len(page) != 2 || page[0].ID != resultIDs[0] || page[1].ID != resultIDs[1] {
			t.Fatalf("first aggregation page = %#v, %v", page, err)
		}
		page, err = repositories.Retention.ListAggregationResults(
			ctx, cutoff.Add(-time.Minute), cutoff.Add(time.Minute), page[1].ReceivedAt, page[1].ID, 2,
		)
		if err != nil || len(page) != 1 || page[0].ID != resultIDs[2] {
			t.Fatalf("resumed aggregation page = %#v, %v", page, err)
		}
		deleted, err := repositories.Retention.DeleteExpiredResults(ctx, cutoff, 1)
		if err != nil || deleted != 1 {
			t.Fatalf("bounded DeleteExpiredResults() = %d, %v", deleted, err)
		}
		if _, err := repositories.Results.GetByID(ctx, resultIDs[0]); !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("expired result lookup = %v", err)
		}
		if _, err := repositories.Results.GetByID(ctx, resultIDs[1]); err != nil {
			t.Fatalf("result at exact cutoff was removed: %v", err)
		}

		return nil
	})
}
