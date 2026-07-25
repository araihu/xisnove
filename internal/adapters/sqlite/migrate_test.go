package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestMigrateFreshDatabaseIsIdempotent(t *testing.T) {
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}

	var version int
	err = db.QueryRowContext(
		ctx,
		"SELECT COALESCE(MAX(version_id), 0) FROM schema_migrations WHERE is_applied = 1",
	).Scan(&version)
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("migration version = %d", version)
	}
}

func TestOpenEnforcesForeignKeys(t *testing.T) {
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	_, err = db.Exec(`
		INSERT INTO sessions (id, admin_id, token_hash, expires_at)
		VALUES ('session-1', 'missing-admin', x'01', '2026-07-25T00:00:00Z')
	`)
	if err == nil {
		t.Fatal("foreign-key violation was accepted")
	}
}
