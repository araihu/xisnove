package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

// This first test fixes the public application boundary before the HTTP
// adapter exists. The implementation intentionally does not exist yet.
func TestOperatorServiceRequiresOwnerForAgentApply(t *testing.T) {
	service := OperatorService{}
	_, err := service.ApplyAgent(context.Background(), ApplyOperatorAgent{})
	if err == nil {
		t.Fatal("ApplyAgent without an owner succeeded")
	}
}

func TestOperatorAgentApplyReplaysHashOnlyAndGuardsCredentialRevocation(t *testing.T) {
	store := newOperatorTestStore(t)
	now := time.Date(2026, 7, 26, 15, 0, 0, 0, time.UTC)
	if err := store.Transact(context.Background(), func(ctx context.Context, repositories Repositories) error {
		location, err := domain.NewLocation("00000000-0000-4000-8000-000000000001", "edge", now)
		if err != nil {
			return err
		}
		return repositories.Locations.Create(ctx, location)
	}); err != nil {
		t.Fatal(err)
	}
	counting := &operatorCountingStore{unit: store}
	service := OperatorService{Store: counting, Credentials: operatorTestHasher{}}
	request := ApplyOperatorAgent{
		Owner: port.ExternalOwner{Key: "default/edge", UID: "uid-1"}, Name: "edge-agent",
		LocationID: "00000000-0000-4000-8000-000000000001", Enabled: true,
		Capabilities:      []domain.AgentCapability{domain.CapabilityHTTP},
		InitialCredential: OperatorInitialCredential{Generation: 1, Credential: "initial-credential-01234567890123456789"},
		IdempotencyKey:    "operator-agent-apply-1",
	}
	first, err := service.ApplyAgent(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if counting.transactions != 1 {
		t.Fatalf("agent apply transactions = %d, want one atomic binding/mutation transaction", counting.transactions)
	}
	replay, err := service.ApplyAgent(context.Background(), request)
	if err != nil || replay != first {
		t.Fatalf("replay = %#v, %v", replay, err)
	}
	changedCredential := request
	changedCredential.InitialCredential.Credential = "different-initial-credential-012345678901234"
	if _, err := service.ApplyAgent(context.Background(), changedCredential); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("changed credential replay = %v, want idempotency conflict", err)
	}
	changedCredential.IdempotencyKey = "operator-agent-apply-different-key"
	if _, err := service.ApplyAgent(context.Background(), changedCredential); !errors.Is(err, ErrConflict) {
		t.Fatalf("same owner with a different initial credential = %v, want conflict", err)
	}
	if first.ExternalID == "" || first.CredentialGeneration != 1 {
		t.Fatalf("state = %#v", first)
	}
	if string(first.ExternalID) == request.InitialCredential.Credential {
		t.Fatal("plaintext credential reached metadata response")
	}
	put := PutOperatorCredential{
		Owner: request.Owner, AgentID: first.ExternalID, Generation: 2,
		Credential: "replacement-credential-012345678901234567", IdempotencyKey: "operator-agent-put-2",
	}
	if err := service.PutAgentCredential(context.Background(), put); err != nil {
		t.Fatal(err)
	}
	if err := service.PutAgentCredential(context.Background(), put); err != nil {
		t.Fatalf("same credential PUT replay: %v", err)
	}
	alternateKeyPut := put
	alternateKeyPut.IdempotencyKey = "operator-agent-put-2-alternate"
	if err := service.PutAgentCredential(context.Background(), alternateKeyPut); err != nil {
		t.Fatalf("same generation and hash with a new key: %v", err)
	}
	changedAlternateKeyPut := alternateKeyPut
	changedAlternateKeyPut.Credential = "different-replacement-credential-01234567890123"
	if err := service.PutAgentCredential(context.Background(), changedAlternateKeyPut); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("changed alternate-key PUT replay = %v, want idempotency conflict", err)
	}
	changedPut := put
	changedPut.Credential = "different-replacement-credential-01234567890123"
	if err := service.PutAgentCredential(context.Background(), changedPut); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("changed credential PUT replay = %v, want idempotency conflict", err)
	}
	revoke := RevokeOperatorCredential{Owner: request.Owner, AgentID: first.ExternalID, Generation: 1, IdempotencyKey: "operator-agent-revoke-1"}
	if err := service.RevokeAgentCredential(context.Background(), revoke); !errors.Is(err, ErrConflict) {
		t.Fatalf("revoke before replacement heartbeat = %v, want conflict", err)
	}
	if err := store.Transact(context.Background(), func(ctx context.Context, repositories Repositories) error {
		updated, err := repositories.Agents.UpdateHeartbeat(ctx, first.ExternalID, 2, "v1", []domain.AgentCapability{domain.CapabilityHTTP}, now.Add(time.Minute))
		if err != nil {
			return err
		}
		if !updated {
			return errors.New("replacement heartbeat was not recorded")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeAgentCredential(context.Background(), revoke); err != nil {
		t.Fatalf("revoke after replacement heartbeat: %v", err)
	}
	if err := service.RevokeAgentCredential(context.Background(), revoke); err != nil {
		t.Fatalf("same revoke replay: %v", err)
	}
	changedRevoke := revoke
	changedRevoke.Generation = 2
	if err := service.RevokeAgentCredential(context.Background(), changedRevoke); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("changed revoke replay = %v, want idempotency conflict", err)
	}

	deleteAgent := DeleteOperatorAgent{Owner: request.Owner, IdempotencyKey: "operator-agent-delete-owner-only"}
	if err := service.DeleteAgent(context.Background(), deleteAgent); err != nil {
		t.Fatalf("owner-only delete: %v", err)
	}
	if err := service.DeleteAgent(context.Background(), deleteAgent); err != nil {
		t.Fatalf("owner-only delete replay: %v", err)
	}
	recreatedOwnerOnly := deleteAgent
	recreatedOwnerOnly.Owner.UID = "uid-2"
	if err := service.DeleteAgent(context.Background(), recreatedOwnerOnly); err != nil {
		t.Fatalf("new UID must have an isolated idempotency identity: %v", err)
	}
	changedDelete := deleteAgent
	changedDelete.ExternalID = "00000000-0000-4000-8000-000000000099"
	if err := service.DeleteAgent(context.Background(), changedDelete); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("changed agent delete replay = %v, want idempotency conflict", err)
	}
	recreatedDelete := DeleteOperatorAgent{
		Owner: port.ExternalOwner{Key: request.Owner.Key, UID: "uid-2"}, ExternalID: first.ExternalID,
		IdempotencyKey: "operator-agent-delete-recreated",
	}
	if err := service.DeleteAgent(context.Background(), recreatedDelete); !errors.Is(err, ErrConflict) {
		t.Fatalf("recreated UID deleting old external ID = %v, want conflict", err)
	}
}

func TestOperatorMonitorOwnerOnlyDeleteReplaysAndRejectsChangedRequest(t *testing.T) {
	store := newOperatorTestStore(t)
	now := time.Date(2026, 7, 26, 16, 0, 0, 0, time.UTC)
	if err := store.Transact(context.Background(), func(ctx context.Context, repositories Repositories) error {
		location, err := domain.NewLocation("00000000-0000-4000-8000-000000000002", "monitor-edge", now)
		if err != nil {
			return err
		}
		return repositories.Locations.Create(ctx, location)
	}); err != nil {
		t.Fatal(err)
	}
	service := OperatorService{Store: store, Credentials: operatorTestHasher{}}
	owner := port.ExternalOwner{Key: "default/check", UID: "monitor-uid-1"}
	state, err := service.ApplyMonitor(context.Background(), ApplyOperatorMonitor{
		Owner: owner, IdempotencyKey: "operator-monitor-apply-1",
		Monitor: ReplaceMonitorCommand{CreateMonitorCommand: CreateMonitorCommand{
			Name: "edge check", LocationID: "00000000-0000-4000-8000-000000000002", RequiredLocation: true,
			Interval: time.Minute, Timeout: 5 * time.Second, FailureThreshold: 2, RecoveryThreshold: 1,
			Probe: domain.ProbeDefinition{Kind: domain.MonitorKindHTTP, HTTP: domain.HTTPProbe{
				Method: "GET", URL: "https://example.test/health", ExpectedStatus: []domain.StatusRange{{Min: 200, Max: 299}},
			}},
		}, Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := DeleteOperatorMonitor{Owner: owner, IdempotencyKey: "operator-monitor-delete-owner-only"}
	if err := service.DeleteMonitor(context.Background(), request); err != nil {
		t.Fatalf("owner-only monitor delete: %v", err)
	}
	if err := service.DeleteMonitor(context.Background(), request); err != nil {
		t.Fatalf("owner-only monitor delete replay: %v", err)
	}
	changed := request
	changed.ExternalID = "00000000-0000-4000-8000-000000000098"
	if err := service.DeleteMonitor(context.Background(), changed); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("changed monitor delete replay = %v, want idempotency conflict", err)
	}
	recreated := DeleteOperatorMonitor{
		Owner: port.ExternalOwner{Key: owner.Key, UID: "monitor-uid-2"}, ExternalID: state.ExternalID,
		IdempotencyKey: "operator-monitor-delete-recreated",
	}
	if err := service.DeleteMonitor(context.Background(), recreated); !errors.Is(err, ErrConflict) {
		t.Fatalf("recreated UID deleting old monitor = %v, want conflict", err)
	}
}

func TestConcurrentOperatorCredentialPUTResolvesWinningIdempotencyRecord(t *testing.T) {
	for _, test := range []struct {
		name        string
		credentials [2]string
		wantReused  int
	}{
		{name: "identical request", credentials: [2]string{"concurrent-credential-012345678901234567890", "concurrent-credential-012345678901234567890"}},
		{name: "changed hash", credentials: [2]string{"concurrent-credential-a-0123456789012345678", "concurrent-credential-b-0123456789012345678"}, wantReused: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			for iteration := range 8 {
				t.Run(fmt.Sprintf("iteration-%d", iteration), func(t *testing.T) {
					base := newOperatorTestStore(t)
					service, owner, agentID := seedOperatorAgent(t, base)
					barrier := newOperatorCredentialBarrierStore(base)
					service.Store = barrier
					start := make(chan struct{})
					errorsByCall := make(chan error, 2)
					for index := range 2 {
						index := index
						go func() {
							<-start
							errorsByCall <- service.PutAgentCredential(context.Background(), PutOperatorCredential{
								Owner: owner, AgentID: agentID, Generation: 2,
								Credential: test.credentials[index], IdempotencyKey: "concurrent-put-key",
							})
						}()
					}
					close(start)
					reused, succeeded := 0, 0
					for range 2 {
						err := <-errorsByCall
						switch {
						case err == nil:
							succeeded++
						case errors.Is(err, ErrIdempotencyKeyReused):
							reused++
						default:
							t.Fatalf("concurrent PUT error = %v", err)
						}
					}
					if reused != test.wantReused || succeeded != 2-test.wantReused {
						t.Fatalf("concurrent results: succeeded=%d reused=%d", succeeded, reused)
					}
					if barrier.forcedLosers.Load() != 1 {
						t.Fatalf("forced CAS losers = %d, want one", barrier.forcedLosers.Load())
					}
				})
			}
		})
	}
}

func seedOperatorAgent(t *testing.T, store UnitOfWork) (OperatorService, port.ExternalOwner, domain.AgentID) {
	t.Helper()
	now := time.Date(2026, 7, 26, 17, 0, 0, 0, time.UTC)
	locationID := domain.LocationID("00000000-0000-4000-8000-000000000003")
	if err := store.Transact(context.Background(), func(ctx context.Context, repositories Repositories) error {
		location, err := domain.NewLocation(locationID, "concurrent-edge", now)
		if err != nil {
			return err
		}
		return repositories.Locations.Create(ctx, location)
	}); err != nil {
		t.Fatal(err)
	}
	service := OperatorService{Store: store, Credentials: operatorTestHasher{}}
	owner := port.ExternalOwner{Key: "default/concurrent-edge", UID: "concurrent-uid"}
	state, err := service.ApplyAgent(context.Background(), ApplyOperatorAgent{
		Owner: owner, Name: "concurrent-edge", LocationID: locationID, Enabled: true,
		Capabilities:      []domain.AgentCapability{domain.CapabilityHTTP},
		InitialCredential: OperatorInitialCredential{Generation: 1, Credential: "initial-concurrent-credential-01234567890123"},
		IdempotencyKey:    "concurrent-agent-apply",
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, owner, state.ExternalID
}

type operatorCredentialBarrierStore struct {
	unit            UnitOfWork
	arrivals        atomic.Int32
	forcedLosers    atomic.Int32
	bothArrived     chan struct{}
	winnerCommitted chan struct{}
	arrivedOnce     sync.Once
	committedOnce   sync.Once
}

func newOperatorCredentialBarrierStore(unit UnitOfWork) *operatorCredentialBarrierStore {
	return &operatorCredentialBarrierStore{unit: unit, bothArrived: make(chan struct{}), winnerCommitted: make(chan struct{})}
}

func (s *operatorCredentialBarrierStore) View(ctx context.Context, callback func(context.Context, Repositories) error) error {
	return s.unit.View(ctx, callback)
}

func (s *operatorCredentialBarrierStore) Transact(ctx context.Context, callback func(context.Context, Repositories) error) error {
	role := s.arrivals.Add(1)
	if role == 2 {
		s.arrivedOnce.Do(func() { close(s.bothArrived) })
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.bothArrived:
	}
	if role == 2 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.winnerCommitted:
		}
	}
	err := s.unit.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		if role != 2 {
			return callback(ctx, repositories)
		}
		wrapped := repositories
		wrapped.Idempotency = &operatorStaleIdempotencyRepository{IdempotencyRepository: repositories.Idempotency}
		wrapped.Management = &operatorStaleAgentRepository{ManagementQueryRepository: repositories.Management}
		wrapped.ManagementCommands = &operatorCredentialBarrierCommands{
			ManagementCommandRepository: repositories.ManagementCommands, store: s,
		}
		return callback(ctx, wrapped)
	})
	if role == 1 {
		s.committedOnce.Do(func() { close(s.winnerCommitted) })
	}
	return err
}

type operatorStaleIdempotencyRepository struct {
	port.IdempotencyRepository
	hidden bool
}

func (r *operatorStaleIdempotencyRepository) Get(
	ctx context.Context,
	principal string,
	operation string,
	key string,
	now time.Time,
) (port.IdempotencyRecord, error) {
	if !r.hidden {
		r.hidden = true
		return port.IdempotencyRecord{}, port.ErrNotFound
	}
	return r.IdempotencyRepository.Get(ctx, principal, operation, key, now)
}

type operatorStaleAgentRepository struct {
	port.ManagementQueryRepository
	stale bool
}

func (r *operatorStaleAgentRepository) GetAgent(ctx context.Context, id domain.AgentID) (domain.Agent, error) {
	agent, err := r.ManagementQueryRepository.GetAgent(ctx, id)
	if err == nil && !r.stale {
		r.stale = true
		agent.CredentialGeneration = 1
	}
	return agent, err
}

type operatorCredentialBarrierCommands struct {
	port.ManagementCommandRepository
	store *operatorCredentialBarrierStore
}

func (r *operatorCredentialBarrierCommands) CreateAgentCredentialGeneration(
	ctx context.Context,
	command port.CreateAgentCredentialGenerationCommand,
) (bool, error) {
	created, err := r.ManagementCommandRepository.CreateAgentCredentialGeneration(ctx, command)
	if err == nil && !created {
		r.store.forcedLosers.Add(1)
	}
	return created, err
}

func newOperatorTestStore(t *testing.T) Store {
	t.Helper()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "operator.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return sqlitestore.NewStore(db)
}

type operatorTestHasher struct{}

func (operatorTestHasher) Hash(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

type operatorCountingStore struct {
	unit         UnitOfWork
	transactions int
}

func (s *operatorCountingStore) View(ctx context.Context, callback func(context.Context, Repositories) error) error {
	return s.unit.View(ctx, callback)
}

func (s *operatorCountingStore) Transact(ctx context.Context, callback func(context.Context, Repositories) error) error {
	s.transactions++
	return s.unit.Transact(ctx, callback)
}
