package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
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
	if err := service.PutAgentCredential(context.Background(), PutOperatorCredential{
		Owner: request.Owner, AgentID: first.ExternalID, Generation: 2,
		Credential: "replacement-credential-012345678901234567", IdempotencyKey: "operator-agent-put-2",
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeAgentCredential(context.Background(), RevokeOperatorCredential{Owner: request.Owner, AgentID: first.ExternalID, Generation: 1}); !errors.Is(err, ErrConflict) {
		t.Fatalf("revoke before replacement heartbeat = %v, want conflict", err)
	}
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
