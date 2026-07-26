package contracttest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

// RunOperatorEdge proves that external ownership is UID-scoped. In particular,
// a recreated external object must never inherit the old object's remote ID.
func RunOperatorEdge(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("operator ownership survives replay, tombstones, and recreation", func(t *testing.T) {
		testOperatorOwnership(t, factory(t))
	})
	t.Run("agent reads latest authenticated active credential generation", func(t *testing.T) {
		testPresentedAgentCredentialGeneration(t, factory(t))
	})
}

func testPresentedAgentCredentialGeneration(t *testing.T, unit port.UnitOfWork) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 12, 30, 0, 0, time.UTC)
	locationID := domain.LocationID("00000000-0000-4000-8000-000000000951")
	location, err := domain.NewLocation(locationID, "credential-edge", now)
	if err != nil {
		t.Fatal(err)
	}
	agentID := domain.AgentID("00000000-0000-4000-8000-000000000952")
	agent, err := domain.NewAgent(domain.NewAgentParams{ID: agentID, LocationID: locationID, Name: "credential-edge", Capabilities: []domain.AgentCapability{domain.CapabilityHTTP}, CredentialGeneration: 1, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := unit.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		if err := repositories.Locations.Create(ctx, location); err != nil {
			return err
		}
		return repositories.Agents.Create(ctx, port.AgentRecord{Agent: agent, CredentialHash: []byte("generation-1")})
	}); err != nil {
		t.Fatal(err)
	}
	readPresented := func() uint64 {
		t.Helper()
		var presented uint64
		if err := unit.View(ctx, func(ctx context.Context, repositories port.Repositories) error {
			record, err := repositories.Agents.Get(ctx, agentID)
			presented = record.PresentedCredentialGeneration
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return presented
	}
	heartbeat := func(generation uint64) {
		t.Helper()
		if err := unit.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
			updated, err := repositories.Agents.UpdateHeartbeat(ctx, agentID, generation, "contract", []domain.AgentCapability{domain.CapabilityHTTP}, now.Add(time.Duration(generation)*time.Minute))
			if err == nil && !updated {
				return errors.New("heartbeat did not update")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	createGeneration := func(expected, generation uint64) {
		t.Helper()
		if err := unit.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
			created, err := repositories.ManagementCommands.CreateAgentCredentialGeneration(ctx, port.CreateAgentCredentialGenerationCommand{
				ExpectedCurrentGeneration: expected,
				Credential:                port.AgentCredentialRecord{AgentID: agentID, Generation: generation, CredentialHash: []byte(fmt.Sprintf("generation-%d", generation)), CreatedAt: now.Add(time.Duration(generation) * time.Second)},
			})
			if err == nil && !created {
				return errors.New("credential generation compare-and-set did not apply")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	revoke := func(generation uint64) {
		t.Helper()
		if err := unit.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
			outcome, err := repositories.ManagementCommands.RevokeAgentCredentialGeneration(ctx, agentID, generation, now.Add(time.Duration(generation+10)*time.Minute))
			if err == nil && outcome != port.CredentialGenerationRevoked {
				return fmt.Errorf("revoke generation %d outcome = %s", generation, outcome)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	createGeneration(1, 2)
	heartbeat(2)
	if got := readPresented(); got != 2 {
		t.Fatalf("presented generation = %d, want 2", got)
	}
	revoke(1)
	createGeneration(2, 3)
	if got := readPresented(); got != 2 {
		t.Fatalf("unseen generation became presented: %d, want 2", got)
	}
	heartbeat(3)
	revoke(2)
	if got := readPresented(); got != 3 {
		t.Fatalf("revoked generation displaced current presentation: %d, want 3", got)
	}
}

func testOperatorOwnership(t *testing.T, unit port.UnitOfWork) {
	t.Helper()
	ctx := context.Background()
	owner := port.ExternalOwner{Key: "clusters/edge-a", UID: "uid-1"}
	recreated := port.ExternalOwner{Key: owner.Key, UID: "uid-2"}
	binding := port.OperatorBinding{Owner: owner, Kind: "agent", ResourceID: "agent-1"}

	bind := func(binding port.OperatorBinding) error {
		return unit.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
			return repositories.Operator.Bind(ctx, binding)
		})
	}
	resolve := func(owner port.ExternalOwner, kind string) (port.OperatorBinding, error) {
		var got port.OperatorBinding
		err := unit.View(ctx, func(ctx context.Context, repositories port.Repositories) error {
			var err error
			got, err = repositories.Operator.Resolve(ctx, owner, kind)
			return err
		})
		return got, err
	}

	if err := bind(binding); err != nil {
		t.Fatalf("first binding: %v", err)
	}
	if err := bind(binding); err != nil {
		t.Fatalf("same-owner replay: %v", err)
	}
	if got, err := resolve(owner, "agent"); err != nil || got != binding {
		t.Fatalf("same owner resolve = %#v, %v", got, err)
	}
	if _, err := resolve(recreated, "agent"); !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("recreated owner resolve error = %v, want not found", err)
	}
	if err := bind(port.OperatorBinding{Owner: recreated, Kind: "agent", ResourceID: "agent-2"}); err != nil {
		t.Fatalf("recreated owner new remote resource: %v", err)
	}
	if err := bind(port.OperatorBinding{Owner: recreated, Kind: "agent", ResourceID: "agent-1"}); !errors.Is(err, port.ErrConflict) {
		t.Fatalf("recreated owner claimed old resource: %v", err)
	}
	if err := bind(port.OperatorBinding{Owner: port.ExternalOwner{Key: "clusters/edge-b", UID: "uid-3"}, Kind: "agent", ResourceID: "agent-1"}); !errors.Is(err, port.ErrConflict) {
		t.Fatalf("resource takeover error = %v, want conflict", err)
	}

	deletedAt := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{deletedAt, deletedAt.Add(time.Minute)} {
		if err := unit.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
			return repositories.Operator.Tombstone(ctx, owner, "agent", at)
		}); err != nil {
			t.Fatalf("tombstone replay: %v", err)
		}
	}
	got, err := resolve(owner, "agent")
	if err != nil || got.DeletedAt == nil || !got.DeletedAt.Equal(deletedAt.Add(time.Minute)) {
		t.Fatalf("tombstoned binding = %#v, %v", got, err)
	}
	if err := bind(binding); err != nil {
		t.Fatalf("owner-only delete recovery: %v", err)
	}
	got, err = resolve(owner, "agent")
	if err != nil || got.DeletedAt != nil || got.ResourceID != "agent-1" {
		t.Fatalf("recovered binding = %#v, %v", got, err)
	}
}
