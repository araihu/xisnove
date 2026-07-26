package tursolocal_test

import (
	"context"
	"path/filepath"
	"testing"

	application "github.com/araihu/xisnove/application/port"
	conformance "github.com/araihu/xisnove/contracttest"
	"github.com/araihu/xisnove/internal/adapters/tursolocal"
)

func TestPersistenceConformance(t *testing.T) {
	factory := func(t *testing.T) application.UnitOfWork {
		t.Helper()
		db, err := tursolocal.Open(
			context.Background(),
			filepath.Join(t.TempDir(), "conformance.turso"),
		)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := tursolocal.Migrate(context.Background(), db); err != nil {
			t.Fatal(err)
		}
		return tursolocal.NewStore(db)
	}
	conformance.Run(t, factory)
	conformance.RunIdempotency(t, factory)
}
