package integration_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
	"github.com/araihu/xisnove/internal/adapters/database"
)

func TestWorkerRecoveryStorageMatrix(t *testing.T) {
	t.Run("SQLite", func(t *testing.T) {
		runDeliveryWorkerRecovery(t, newFileStorageHarness(t, database.ProfileSQLite))
	})
	t.Run("TursoLocal", func(t *testing.T) {
		runDeliveryWorkerRecovery(t, newFileStorageHarness(t, database.ProfileTursoLocal))
	})
	t.Run("Postgres", func(t *testing.T) {
		runDeliveryWorkerRecovery(t, newPostgresStorageHarness(t))
	})
	t.Run("TursoCloud", func(t *testing.T) {
		runDeliveryWorkerRecovery(t, newTursoCloudStorageHarness(t))
	})
}

func runDeliveryWorkerRecovery(t *testing.T, harness *storageHarness) {
	t.Helper()
	ctx := context.Background()
	ids := &matrixIDs{}
	tokens := &matrixTokens{}
	sealer := recoverySealer{}

	databaseNow, err := harness.primary.Store.Repositories().Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatalf("read database time: %v", err)
	}
	configuration := application.NewConfigurationService(
		harness.primary.Store,
		func() time.Time { return databaseNow },
		ids.New,
	)
	location, err := configuration.CreateLocation(ctx, application.CreateLocationCommand{Name: "worker recovery"})
	if err != nil {
		t.Fatal(err)
	}
	monitor, err := configuration.CreateMonitor(ctx, application.CreateMonitorCommand{
		Name: "worker recovery monitor", LocationID: location.ID, RequiredLocation: true,
		Interval: time.Minute, Timeout: 5 * time.Second,
		FailureThreshold: 1, RecoveryThreshold: 1,
		Probe: domain.ProbeDefinition{Kind: domain.MonitorKindHTTP, HTTP: domain.HTTPProbe{
			Method: "GET", URL: "https://example.test/health",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	admin := application.NewNotificationAdminService(application.NotificationAdminServiceConfig{
		Store: harness.primary.Store, Sealer: sealer,
		Now: func() time.Time { return databaseNow }, NewID: ids.New,
	})
	channel, err := admin.CreateChannel(ctx, application.PutNotificationChannelCommand{
		Name: "worker recovery channel", Enabled: true,
		Config: application.NotificationChannelConfig{
			Kind: domain.NotificationChannelShoutrrr, ShoutrrrServiceURL: "generic://example.test",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.CreateRoute(ctx, application.PutNotificationRouteCommand{
		Name: "worker recovery route", ChannelID: channel.ID, MonitorID: &monitor.ID,
		Actions: []domain.NotificationAction{domain.NotificationOpen}, Enabled: true,
		Template: "{{.MonitorName}} is {{.State}}",
	}); err != nil {
		t.Fatal(err)
	}
	decision := domain.DecideIncident(nil, monitor.ID, domain.HealthDown, databaseNow, func() domain.IncidentID {
		return domain.IncidentID(ids.New())
	})
	if err := harness.primary.Store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		return application.RecordIncidentTransition(ctx, repositories, decision, databaseNow, ids.New)
	}); err != nil {
		t.Fatal(err)
	}

	var providerEffects atomic.Int32
	var observedOwner string
	transport := recoveryTransportFunc(func(context.Context, application.TransportDelivery) application.TransportResult {
		effect := providerEffects.Add(1)
		records, listErr := harness.primary.Store.Repositories().NotificationOutbox.List(ctx, 10, 0)
		if listErr != nil || len(records) != 1 {
			t.Errorf("read claim during provider effect: records=%#v error=%v", records, listErr)
		} else {
			observedOwner = records[0].ClaimOwner
		}
		return application.NewTransportResult(
			application.TransportDelivered, "", "", fmt.Sprintf("provider-effect-%d", effect),
		)
	})
	crashBeforeFinalize := errors.New("injected worker loss before finalize")
	workerAStore := &failNthTransactionUnitOfWork{
		next: harness.primary.Store, failAt: 2, failure: crashBeforeFinalize,
	}
	workerA, err := application.NewDeliveryWorker(application.DeliveryWorkerConfig{
		Store: workerAStore, Sealer: sealer, Tokens: tokens, NewID: ids.New,
		Owner: "worker-a", BatchSize: 1,
		Transports: map[domain.NotificationChannelKind]application.NotificationTransport{
			domain.NotificationChannelShoutrrr: transport,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if count, runErr := workerA.RunOnce(ctx); count != 1 || !errors.Is(runErr, crashBeforeFinalize) {
		t.Fatalf("worker-a loss after provider effect: count=%d error=%v", count, runErr)
	}
	if providerEffects.Load() != 1 || observedOwner != "worker-a" {
		t.Fatalf("first provider effect: count=%d owner=%q", providerEffects.Load(), observedOwner)
	}
	records, err := harness.primary.Store.Repositories().NotificationOutbox.List(ctx, 10, 0)
	if err != nil || len(records) != 1 {
		t.Fatalf("delivery after worker-a loss: records=%#v error=%v", records, err)
	}
	claimed := records[0]
	attempts, err := harness.primary.Store.Repositories().NotificationOutbox.ListAttempts(ctx, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.State != domain.DeliveryClaimed || claimed.ClaimOwner != "worker-a" ||
		claimed.AttemptCount != 0 || len(attempts) != 0 || claimed.DeliveredAt != nil {
		t.Fatalf("unfinalized provider effect became durable: delivery=%#v attempts=%#v", claimed, attempts)
	}

	workerB, err := application.NewDeliveryWorker(application.DeliveryWorkerConfig{
		Store: harness.secondary.Store, Sealer: sealer, Tokens: tokens, NewID: ids.New,
		Owner: "worker-b", BatchSize: 1,
		Transports: map[domain.NotificationChannelKind]application.NotificationTransport{
			domain.NotificationChannelShoutrrr: transport,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if count, err := workerB.RunOnce(ctx); err != nil || count != 0 {
		t.Fatalf("worker-b claimed active worker-a lease: count=%d error=%v", count, err)
	}
	if providerEffects.Load() != 1 {
		t.Fatalf("provider called while worker-a lease remained active: effects=%d", providerEffects.Load())
	}

	expiredAt := databaseNow.Add(-time.Second)
	query := "UPDATE notification_outbox SET claim_expires_at = ? WHERE id = ?"
	expiryValue := any(expiredAt.Format(time.RFC3339Nano))
	if harness.config.Profile == database.ProfilePostgres {
		query = "UPDATE notification_outbox SET claim_expires_at = $1 WHERE id = $2"
		expiryValue = expiredAt
	}
	result, err := harness.primary.DB.ExecContext(ctx, query, expiryValue, claimed.ID)
	if err != nil {
		t.Fatalf("expire worker-a claim at database-time boundary: %v", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("expired worker-a claims = %d, %v", affected, err)
	}

	if count, err := workerB.RunOnce(ctx); err != nil || count != 1 {
		t.Fatalf("worker-b recovery after lease expiry: count=%d error=%v", count, err)
	}
	if providerEffects.Load() != 2 || observedOwner != "worker-b" {
		t.Fatalf("recovered provider effect: count=%d owner=%q", providerEffects.Load(), observedOwner)
	}
	record, err := harness.primary.Store.Repositories().NotificationOutbox.Get(ctx, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	attempts, err = harness.primary.Store.Repositories().NotificationOutbox.ListAttempts(ctx, claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != domain.DeliveryDelivered || record.AttemptCount != 1 || len(attempts) != 1 {
		t.Fatalf("recovered delivery = %#v, attempts = %#v", record, attempts)
	}
	if attempts[0].Ordinal != 1 || attempts[0].Outcome != port.NotificationAttemptDelivered ||
		attempts[0].ProviderReceipt != "provider-effect-2" {
		t.Fatalf("recovered attempt = %#v", attempts[0])
	}
}

type failNthTransactionUnitOfWork struct {
	next    port.UnitOfWork
	failAt  int32
	failure error
	calls   atomic.Int32
}

func (u *failNthTransactionUnitOfWork) View(
	ctx context.Context,
	callback func(context.Context, port.Repositories) error,
) error {
	return u.next.View(ctx, callback)
}

func (u *failNthTransactionUnitOfWork) Transact(
	ctx context.Context,
	callback func(context.Context, port.Repositories) error,
) error {
	if u.calls.Add(1) == u.failAt {
		return u.failure
	}
	return u.next.Transact(ctx, callback)
}

type recoverySealer struct{}

func (recoverySealer) ActiveVersion() uint32       { return 1 }
func (recoverySealer) CanOpen(version uint32) bool { return version == 1 }
func (recoverySealer) Seal(_ context.Context, _ port.ConfigIdentity, plaintext []byte) (port.SealedConfig, error) {
	return port.SealedConfig{KeyVersion: 1, Ciphertext: append([]byte(nil), plaintext...)}, nil
}
func (recoverySealer) Open(_ context.Context, _ port.ConfigIdentity, sealed port.SealedConfig) ([]byte, error) {
	if sealed.KeyVersion != 1 {
		return nil, fmt.Errorf("unsupported recovery key version %d", sealed.KeyVersion)
	}
	return append([]byte(nil), sealed.Ciphertext...), nil
}

type recoveryTransportFunc func(context.Context, application.TransportDelivery) application.TransportResult

func (f recoveryTransportFunc) Send(ctx context.Context, delivery application.TransportDelivery) application.TransportResult {
	return f(ctx, delivery)
}
