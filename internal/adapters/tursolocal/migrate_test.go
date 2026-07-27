package tursolocal_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/araihu/xisnove/internal/adapters/tursolocal"
)

func TestLocalTursoExposesDatabaseFileForSingletonMigrationOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local-turso.db")
	db, err := tursolocal.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rows, err := db.QueryContext(context.Background(), "PRAGMA database_list")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var sequence int
		var name, actualPath string
		if err := rows.Scan(&sequence, &name, &actualPath); err != nil {
			t.Fatal(err)
		}
		if name == "main" {
			if actualPath == "" {
				t.Fatal("local Turso main database path is empty")
			}
			return
		}
	}
	t.Fatal("local Turso main database is absent from PRAGMA database_list")
}
