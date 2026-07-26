package contracttest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/araihu/xisnove/application/port"
)

// RunOperatorEdge proves that external ownership is UID-scoped. In particular,
// a recreated external object must never inherit the old object's remote ID.
func RunOperatorEdge(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("operator ownership survives replay, tombstones, and recreation", func(t *testing.T) {
		testOperatorOwnership(t, factory(t))
	})
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
