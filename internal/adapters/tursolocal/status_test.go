package tursolocal_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/contracttest"
	"github.com/araihu/xisnove/internal/adapters/tursolocal"
)

func TestPublicStatusPersistenceConformance(t *testing.T) {
	ctx := context.Background()
	contracttest.RunPublicStatus(t, func(t *testing.T) (port.UnitOfWork, port.PublicStatusUnitOfWork) {
		db, err := tursolocal.Open(ctx, filepath.Join(t.TempDir(), "status.turso"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := tursolocal.Migrate(ctx, db); err != nil {
			t.Fatal(err)
		}
		return tursolocal.NewStore(db), tursolocal.NewPublicStatusUnitOfWork(db)
	})
}
