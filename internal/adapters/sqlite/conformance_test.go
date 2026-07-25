package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/araihu/xisnove/internal/adapters/conformance"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
	"github.com/araihu/xisnove/internal/application"
)

func TestPersistenceConformance(t *testing.T) {
	conformance.Run(t, func(t *testing.T) application.Store {
		t.Helper()
		db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "conformance.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := sqlitestore.Migrate(context.Background(), db); err != nil {
			t.Fatal(err)
		}
		return sqlitestore.NewStore(db)
	})
}
