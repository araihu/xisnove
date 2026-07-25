package main

import (
	"context"
	"path/filepath"
	"testing"

	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestMigrateCommandCreatesCurrentDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xisnove.db")
	if err := run(context.Background(), []string{
		"db", "migrate", "--database", path,
	}); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sqlitestore.Ready(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}
