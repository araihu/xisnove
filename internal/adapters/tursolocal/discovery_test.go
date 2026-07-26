package tursolocal_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/contracttest"
	"github.com/araihu/xisnove/internal/adapters/tursolocal"
)

func TestDiscoveryPersistenceConformance(t *testing.T) {
	contracttest.RunDiscovery(t, func(t *testing.T) port.UnitOfWork {
		db, err := tursolocal.Open(context.Background(), filepath.Join(t.TempDir(), "discovery.turso"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := tursolocal.Migrate(context.Background(), db); err != nil {
			t.Fatal(err)
		}
		return tursolocal.NewStore(db)
	})
}
