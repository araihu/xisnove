package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

func TestDeliveryWorkerDeliversOutsideTransactionAndClearsConfiguration(t *testing.T) {
	ctx := context.Background()
	fixture := newWorkerFixture(t, ctx)
	observed := make(chan TransportDelivery, 1)
	transport := transportFunc(func(_ context.Context, delivery TransportDelivery) TransportResult {
		if fixture.store.inTransaction.Load() != 0 {
			t.Error("transport called inside transaction")
		}
		observed <- delivery
		return NewTransportResult(TransportDelivered, "", "", "receipt-1")
	})
	worker := fixture.worker(t, transport, nil)
	count, err := worker.RunOnce(ctx)
	if err != nil || count != 1 {
		t.Fatalf("RunOnce() = %d, %v", count, err)
	}
	delivery := <-observed
	if delivery.Message != "router is down" || delivery.Title != "router is down" || delivery.Snapshot.IncidentID == "" {
		t.Fatalf("delivery = %#v", delivery)
	}
	for _, value := range delivery.Configuration {
		if value != 0 {
			t.Fatalf("decrypted configuration was retained: %q", delivery.Configuration)
		}
	}
	record := fixture.delivery(t, ctx)
	attempts := fixture.attempts(t, ctx)
	if record.State != domain.DeliveryDelivered || record.AttemptCount != 1 || len(attempts) != 1 || attempts[0].ProviderReceipt != "receipt-1" {
		t.Fatalf("record = %#v, attempts = %#v", record, attempts)
	}
}

func TestDeliveryWorkerObservesCommittedBoundedOutcomeWithoutDiagnosticPayload(t *testing.T) {
	ctx := context.Background()
	fixture := newWorkerFixture(t, ctx)
	observed := make(chan DeliveryObservation, 1)
	worker := fixture.worker(t, transportFunc(func(context.Context, TransportDelivery) TransportResult {
		return NewTransportResult(
			TransportTransientFailure,
			"provider_retryable",
			"secret provider diagnostic",
			"secret provider receipt",
		)
	}), func(config *DeliveryWorkerConfig) {
		config.BatchSize = 1
		config.ObserveDelivery = func(observation DeliveryObservation) {
			if fixture.store.inTransaction.Load() != 0 {
				t.Error("delivery observed before transaction committed")
			}
			observed <- observation
		}
	})

	if count, err := worker.RunOnce(ctx); err != nil || count != 1 {
		t.Fatalf("RunOnce() = %d, %v", count, err)
	}
	want := DeliveryObservation{
		AttemptOutcome:  DeliveryAttemptTransientFailure,
		FinalOutcome:    DeliveryFinalRetry,
		DiagnosticClass: DeliveryDiagnosticProvider,
	}
	select {
	case got := <-observed:
		if got != want {
			t.Fatalf("delivery observation = %#v, want %#v", got, want)
		}
		serialized := fmt.Sprint(got)
		if strings.Contains(serialized, "secret provider diagnostic") || strings.Contains(serialized, "secret provider receipt") {
			t.Fatalf("delivery observation leaked provider payload: %q", serialized)
		}
	default:
		t.Fatal("delivery outcome was not observed")
	}
	select {
	case extra := <-observed:
		t.Fatalf("extra delivery observation = %#v", extra)
	default:
	}
}

func TestDeliveryWorkerRetriesCapsAttemptsAndPreservesReplayOrdinals(t *testing.T) {
	ctx := context.Background()
	fixture := newWorkerFixture(t, ctx)
	var calls atomic.Int32
	transport := transportFunc(func(context.Context, TransportDelivery) TransportResult {
		if calls.Add(1) == 1 {
			return NewTransportResult(TransportTransientFailure, "provider_retryable", "temporary", "")
		}
		return NewTransportResult(TransportDelivered, "", "", "")
	})
	worker := fixture.worker(t, transport, func(config *DeliveryWorkerConfig) {
		config.BackoffBase = time.Millisecond
		config.BackoffCap = time.Millisecond
	})
	if count, err := worker.RunOnce(ctx); err != nil || count != 1 {
		t.Fatalf("first RunOnce() = %d, %v", count, err)
	}
	if got := fixture.delivery(t, ctx); got.State != domain.DeliveryRetrying || got.AttemptCount != 1 {
		t.Fatalf("retry record = %#v", got)
	}
	time.Sleep(5 * time.Millisecond)
	if count, err := worker.RunOnce(ctx); err != nil || count != 1 {
		t.Fatalf("second RunOnce() = %d, %v", count, err)
	}
	if got := fixture.delivery(t, ctx); got.State != domain.DeliveryDelivered || got.AttemptCount != 2 {
		t.Fatalf("delivered record = %#v", got)
	}

	permanentFixture := newWorkerFixture(t, ctx)
	permanentWorker := permanentFixture.worker(t, transportFunc(func(context.Context, TransportDelivery) TransportResult {
		return NewTransportResult(TransportTransientFailure, "provider_retryable", "temporary", "")
	}), func(config *DeliveryWorkerConfig) { config.MaxAttempts = 1 })
	if _, err := permanentWorker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := permanentFixture.delivery(t, ctx); got.State != domain.DeliveryPermanent || got.LastErrorClass != "attempt_limit_exceeded" {
		t.Fatalf("capped record = %#v", got)
	}
	databaseNow, err := permanentFixture.repositories.Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := permanentFixture.repositories.NotificationOutbox.Replay(ctx, permanentFixture.deliveryID, databaseNow); err != nil || !changed {
		t.Fatalf("Replay() = %v, %v", changed, err)
	}
	successWorker := permanentFixture.worker(t, transportFunc(func(context.Context, TransportDelivery) TransportResult {
		return NewTransportResult(TransportDelivered, "", "", "")
	}), nil)
	if _, err := successWorker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	attempts := permanentFixture.attempts(t, ctx)
	if len(attempts) != 2 || attempts[0].Ordinal != 1 || attempts[1].Ordinal != 2 {
		t.Fatalf("replay attempts = %#v", attempts)
	}
}

func TestDeliveryWorkerContainsDeadlinePanicAndLostLease(t *testing.T) {
	ctx := context.Background()
	deadlineFixture := newWorkerFixture(t, ctx)
	deadlineWorker := deadlineFixture.worker(t, transportFunc(func(ctx context.Context, _ TransportDelivery) TransportResult {
		<-ctx.Done()
		return contextResultForTest(ctx.Err())
	}), func(config *DeliveryWorkerConfig) { config.SendTimeout = 10 * time.Millisecond })
	if _, err := deadlineWorker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := deadlineFixture.delivery(t, ctx); got.State != domain.DeliveryRetrying || got.LastErrorClass != "deadline_exceeded" {
		t.Fatalf("deadline record = %#v", got)
	}

	panicFixture := newWorkerFixture(t, ctx)
	panicWorker := panicFixture.worker(t, transportFunc(func(context.Context, TransportDelivery) TransportResult {
		panic("provider secret")
	}), nil)
	if _, err := panicWorker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := panicFixture.delivery(t, ctx); got.State != domain.DeliveryRetrying || got.LastErrorClass != "transport_panic" || got.LastDiagnostic != "notification transport panicked" {
		t.Fatalf("panic record = %#v", got)
	}
	invalidFixture := newWorkerFixture(t, ctx)
	invalidWorker := invalidFixture.worker(t, transportFunc(func(_ context.Context, delivery TransportDelivery) TransportResult {
		return TransportResult{Outcome: "corrupt", Diagnostic: delivery.Message}
	}), nil)
	if _, err := invalidWorker.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := invalidFixture.delivery(t, ctx); got.State != domain.DeliveryPermanent || got.LastErrorClass != "transport_invalid_result" || got.LastDiagnostic != "<redacted>" {
		t.Fatalf("invalid transport record = %#v", got)
	}

	leaseFixture := newWorkerFixture(t, ctx)
	var leaseCalls atomic.Int32
	leaseWorker := leaseFixture.worker(t, transportFunc(func(context.Context, TransportDelivery) TransportResult {
		if leaseCalls.Add(1) == 1 {
			if _, err := leaseFixture.fixture.db.ExecContext(ctx, `UPDATE notification_outbox SET claim_token_hash = x'0102' WHERE id = ?`, leaseFixture.deliveryID); err != nil {
				t.Error(err)
			}
		}
		return NewTransportResult(TransportDelivered, "", "", "")
	}), nil)
	if _, err := leaseWorker.RunOnce(ctx); !errors.Is(err, ErrNotificationLeaseLost) {
		t.Fatalf("lost lease error = %v", err)
	}
	if attempts := leaseFixture.attempts(t, ctx); len(attempts) != 0 {
		t.Fatalf("lost lease attempts = %#v", attempts)
	}
	if _, err := leaseFixture.fixture.db.ExecContext(ctx, `UPDATE notification_outbox SET claim_expires_at = ? WHERE id = ?`, time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), leaseFixture.deliveryID); err != nil {
		t.Fatal(err)
	}
	if count, err := leaseWorker.RunOnce(ctx); err != nil || count != 1 {
		t.Fatalf("post-response recovery RunOnce() = %d, %v", count, err)
	}
	if attempts := leaseFixture.attempts(t, ctx); leaseCalls.Load() != 2 || len(attempts) != 1 || attempts[0].Ordinal != 1 {
		t.Fatalf("duplicate risk calls = %d, attempts = %#v", leaseCalls.Load(), attempts)
	}
}

func TestDeliveryWorkerBoundsParallelCallsRecoversExpiredClaimAndStops(t *testing.T) {
	ctx := context.Background()
	fixture := newWorkerFixture(t, ctx)
	original := fixture.delivery(t, ctx)
	for index := 1; index < 8; index++ {
		clone := original
		clone.ID = domain.NotificationDeliveryID(fmt.Sprintf("delivery-%d", index))
		clone.DedupeKey = fmt.Sprintf("dedupe-%d", index)
		clone.IncidentEventID = fmt.Sprintf("event-%d", index)
		clone.RenderSnapshot.EventID = clone.IncidentEventID
		if err := fixture.repositories.Incidents.AppendEvent(ctx, domain.IncidentEvent{
			ID: clone.IncidentEventID, IncidentID: clone.RenderSnapshot.IncidentID,
			Action: domain.NotificationChange, PreviousState: domain.HealthDown,
			State: domain.HealthDown, Severity: domain.IncidentCritical,
			CreatedAt: original.CreatedAt.Add(time.Duration(index) * time.Nanosecond),
		}); err != nil {
			t.Fatalf("insert event %d: %v", index, err)
		}
		if inserted, err := fixture.repositories.NotificationOutbox.Insert(ctx, clone); err != nil || !inserted {
			t.Fatalf("insert clone %d = %v, %v", index, inserted, err)
		}
	}
	var active, maximum atomic.Int32
	release := make(chan struct{})
	transport := transportFunc(func(context.Context, TransportDelivery) TransportResult {
		value := active.Add(1)
		defer active.Add(-1)
		for {
			old := maximum.Load()
			if value <= old || maximum.CompareAndSwap(old, value) {
				break
			}
		}
		<-release
		return NewTransportResult(TransportDelivered, "", "", "")
	})
	worker := fixture.worker(t, transport, func(config *DeliveryWorkerConfig) {
		config.BatchSize = 8
		config.Concurrency = 2
	})
	done := make(chan error, 1)
	go func() {
		_, err := worker.RunOnce(ctx)
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	for maximum.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if maximum.Load() != 2 {
		t.Fatalf("maximum parallel calls = %d", maximum.Load())
	}

	recoveryFixture := newWorkerFixture(t, ctx)
	recoveryWorker := recoveryFixture.worker(t, transportFunc(func(context.Context, TransportDelivery) TransportResult {
		return NewTransportResult(TransportDelivered, "", "", "")
	}), nil)
	if _, _, err := recoveryWorker.claim(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := recoveryFixture.fixture.db.ExecContext(ctx, `UPDATE notification_outbox SET claim_expires_at = ? WHERE id = ?`, time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), recoveryFixture.deliveryID); err != nil {
		t.Fatal(err)
	}
	if count, err := recoveryWorker.RunOnce(ctx); err != nil || count != 1 {
		t.Fatalf("recovery RunOnce() = %d, %v", count, err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- recoveryWorker.Run(runCtx) }()
	cancel()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after cancellation")
	}
}

type workerFixture struct {
	fixture      projectionFixture
	store        *observedUnitOfWork
	repositories port.Repositories
	sealer       port.ConfigSealer
	deliveryID   domain.NotificationDeliveryID
	ids          func() string
}

func newWorkerFixture(t *testing.T, ctx context.Context) workerFixture {
	t.Helper()
	fixture := newProjectionFixture(t, ctx)
	sealer := plainTestSealer{}
	channel, err := fixture.repositories.NotificationChannels.Get(ctx, fixture.route.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	configuration, _ := json.Marshal(map[string]string{"serviceUrl": "generic://example.test"})
	sealed, err := sealer.Seal(ctx, port.ConfigIdentity{ChannelID: channel.Channel.ID, Kind: channel.Channel.Kind}, configuration)
	if err != nil {
		t.Fatal(err)
	}
	channel.EncryptedConfig = sealed.Ciphertext
	channel.KeyVersion = sealed.KeyVersion
	if changed, err := fixture.repositories.NotificationChannels.Update(ctx, channel); err != nil || !changed {
		t.Fatalf("update channel = %v, %v", changed, err)
	}
	projectState(t, ctx, fixture, domain.HealthDown, fixture.now, true, monotonicIDs())
	records := outboxRecords(t, ctx, fixture)
	ids := concurrentIDs()
	return workerFixture{
		fixture: fixture, store: &observedUnitOfWork{next: fixture.store},
		repositories: fixture.repositories, sealer: sealer,
		deliveryID: records[0].ID, ids: ids,
	}
}

func (f workerFixture) worker(t *testing.T, transport NotificationTransport, customize func(*DeliveryWorkerConfig)) *DeliveryWorker {
	t.Helper()
	config := DeliveryWorkerConfig{
		Store: f.store, Sealer: f.sealer, Tokens: &workerTokenIssuer{},
		NewID: f.ids, Owner: "worker-1",
		Transports: map[domain.NotificationChannelKind]NotificationTransport{domain.NotificationChannelShoutrrr: transport},
		BatchSize:  20, Concurrency: 4, LeaseDuration: 200 * time.Millisecond,
		PollInterval: 5 * time.Millisecond, SendTimeout: 50 * time.Millisecond,
		MaxAttempts: 4, BackoffBase: time.Millisecond, BackoffCap: time.Second,
		Jitter: func() float64 { return 0 },
	}
	if customize != nil {
		customize(&config)
	}
	worker, err := NewDeliveryWorker(config)
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func (f workerFixture) delivery(t *testing.T, ctx context.Context) port.NotificationOutboxRecord {
	t.Helper()
	record, err := f.repositories.NotificationOutbox.Get(ctx, f.deliveryID)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func (f workerFixture) attempts(t *testing.T, ctx context.Context) []port.NotificationDeliveryAttemptRecord {
	t.Helper()
	attempts, err := f.repositories.NotificationOutbox.ListAttempts(ctx, f.deliveryID)
	if err != nil {
		t.Fatal(err)
	}
	return attempts
}

type observedUnitOfWork struct {
	next          port.UnitOfWork
	inTransaction atomic.Int32
}

func (s *observedUnitOfWork) View(ctx context.Context, callback func(context.Context, port.Repositories) error) error {
	return s.next.View(ctx, callback)
}

func (s *observedUnitOfWork) Transact(ctx context.Context, callback func(context.Context, port.Repositories) error) error {
	return s.next.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		s.inTransaction.Add(1)
		defer s.inTransaction.Add(-1)
		return callback(ctx, repositories)
	})
}

type transportFunc func(context.Context, TransportDelivery) TransportResult

func (function transportFunc) Send(ctx context.Context, delivery TransportDelivery) TransportResult {
	return function(ctx, delivery)
}

type workerTokenIssuer struct{ value atomic.Uint64 }

func (issuer *workerTokenIssuer) New() (IssuedToken, error) {
	raw := fmt.Sprintf("worker-token-%d", issuer.value.Add(1))
	digest := sha256.Sum256([]byte(raw))
	return IssuedToken{Raw: raw, Hash: digest[:]}, nil
}

func (*workerTokenIssuer) Hash(raw string) []byte {
	digest := sha256.Sum256([]byte(raw))
	return digest[:]
}

func concurrentIDs() func() string {
	var value atomic.Uint64
	return func() string { return fmt.Sprintf("10000000-0000-4000-8000-%012d", value.Add(1)) }
}

func contextResultForTest(err error) TransportResult {
	class := "context_canceled"
	if errors.Is(err, context.DeadlineExceeded) {
		class = "deadline_exceeded"
	}
	return NewTransportResult(TransportTransientFailure, class, err.Error(), "")
}

type plainTestSealer struct{}

func (plainTestSealer) ActiveVersion() uint32       { return 1 }
func (plainTestSealer) CanOpen(version uint32) bool { return version == 1 }
func (plainTestSealer) Seal(_ context.Context, _ port.ConfigIdentity, plaintext []byte) (port.SealedConfig, error) {
	return port.SealedConfig{KeyVersion: 1, Ciphertext: append([]byte(nil), plaintext...)}, nil
}
func (plainTestSealer) Open(_ context.Context, _ port.ConfigIdentity, sealed port.SealedConfig) ([]byte, error) {
	return append([]byte(nil), sealed.Ciphertext...), nil
}
