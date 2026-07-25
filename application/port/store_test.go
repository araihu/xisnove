package port_test

import (
	"context"
	"testing"

	"github.com/araihu/xisnove/application/port"
)

type contextKey struct{}

type recordingUnitOfWork struct{}

func (recordingUnitOfWork) View(
	ctx context.Context,
	fn func(context.Context, port.Repositories) error,
) error {
	return fn(ctx, port.Repositories{})
}

func (recordingUnitOfWork) Transact(
	ctx context.Context,
	fn func(context.Context, port.Repositories) error,
) error {
	return fn(ctx, port.Repositories{})
}

func TestUnitOfWorkPropagatesCallerContext(t *testing.T) {
	want := context.WithValue(context.Background(), contextKey{}, "caller")
	uow := recordingUnitOfWork{}

	for name, run := range map[string]func(context.Context, func(context.Context, port.Repositories) error) error{
		"view":     uow.View,
		"transact": uow.Transact,
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(want, func(got context.Context, _ port.Repositories) error {
				if got != want {
					t.Fatal("callback context is not the caller context")
				}
				return nil
			}); err != nil {
				t.Fatalf("UnitOfWork callback: %v", err)
			}
		})
	}
}
