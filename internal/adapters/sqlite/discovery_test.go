package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/contracttest"
	"github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestDiscoveryPersistenceConformance(t *testing.T) {
	contracttest.RunDiscovery(t, func(t *testing.T) port.UnitOfWork {
		db, err := sqlite.Open(filepath.Join(t.TempDir(), "discovery.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := sqlite.Migrate(context.Background(), db); err != nil {
			t.Fatal(err)
		}
		return sqlite.NewStore(db)
	})
}
