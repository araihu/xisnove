package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	migrations "github.com/araihu/xisnove/db/migrations/sqlite"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
	"github.com/pressly/goose/v3"
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
	if version != 4 {
		t.Fatalf("migration version = %d", version)
	}
}

func TestProtocolBreadthMigrationPreservesHTTPMonitor(t *testing.T) {
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	goose.SetBaseFS(migrations.Files)
	goose.SetTableName("schema_migrations")
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpToContext(context.Background(), db, ".", 1); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		INSERT INTO locations (id, name, created_at)
		VALUES ('location-1', 'public', '2026-07-25T12:00:00Z');
		INSERT INTO monitors (
		  id, name, kind, interval_ms, timeout_ms, failure_threshold,
		  recovery_threshold, http_json, enabled, next_run_at, created_at, updated_at
		) VALUES (
		  'monitor-1', 'website', 'http', 60000, 5000, 3, 2,
		  '{"Method":"GET","URL":"https://example.com","ExpectedStatus":[{"Min":200,"Max":299}]}',
		  1, '2026-07-25T12:00:00Z', '2026-07-25T12:00:00Z', '2026-07-25T12:00:00Z'
		)
	`)
	if err != nil {
		t.Fatal(err)
	}

	if err := sqlitestore.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var kind string
	var probeJSON []byte
	if err := db.QueryRow(
		"SELECT kind, probe_json FROM monitors WHERE id = 'monitor-1'",
	).Scan(&kind, &probeJSON); err != nil {
		t.Fatal(err)
	}
	if kind != "http" || len(probeJSON) == 0 {
		t.Fatalf("kind=%q probe=%s", kind, probeJSON)
	}
	var violations int
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		violations++
	}
	if violations != 0 {
		t.Fatalf("foreign-key violations = %d", violations)
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
