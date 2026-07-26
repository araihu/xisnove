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
	if version != 10 {
		t.Fatalf("migration version = %d", version)
	}
}

func TestLocationLifecycleMigrationUpgradesVersionSevenAndDowngrades(t *testing.T) {
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "location-v8.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	provider, err := goose.NewProvider(
		goose.DialectSQLite3,
		db,
		migrations.Files,
		goose.WithTableName("schema_migrations"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 7); err != nil {
		t.Fatal(err)
	}
	const createdAt = "2026-07-26T04:00:00Z"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO locations (id, name, created_at)
		VALUES ('00000000-0000-4000-8000-000000000800', 'upgrade-v8', ?)
	`, createdAt); err != nil {
		t.Fatal(err)
	}
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	var enabled int
	var updatedAt string
	if err := db.QueryRowContext(ctx, `
		SELECT enabled, updated_at FROM locations
		WHERE id = '00000000-0000-4000-8000-000000000800'
	`).Scan(&enabled, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if enabled != 1 || updatedAt != createdAt {
		t.Fatalf("upgraded location enabled=%d updated_at=%q", enabled, updatedAt)
	}
	if _, err := provider.DownTo(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "SELECT enabled, updated_at FROM locations"); err == nil {
		t.Fatal("location lifecycle columns remained after downgrade")
	}
}

func TestAgentCredentialMigrationDowngradesWithForeignKeysEnabled(t *testing.T) {
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "downgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO locations (id, name, enabled, created_at, updated_at)
		VALUES ('00000000-0000-4000-8000-000000000701', 'downgrade', 1, '2026-07-26T00:00:00Z', '2026-07-26T00:00:00Z');
		INSERT INTO agents (
			id, location_id, name, credential_hash, credential_generation,
			capabilities_json, created_at, updated_at
		) VALUES (
			'00000000-0000-4000-8000-000000000702',
			'00000000-0000-4000-8000-000000000701', 'agent', X'07', 1,
			'["http"]', '2026-07-26T00:00:00Z', '2026-07-26T00:00:00Z'
		);
		INSERT INTO agent_credentials (
			agent_id, generation, credential_hash, created_at
		) VALUES (
			'00000000-0000-4000-8000-000000000702', 1, X'07', '2026-07-26T00:00:00Z'
		);
	`); err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(
		goose.DialectSQLite3,
		db,
		migrations.Files,
		goose.WithTableName("schema_migrations"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
	var version, foreignKeys, agentCount int
	if err := db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version_id), 0)
		FROM schema_migrations
		WHERE is_applied = 1
	`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM agents").Scan(&agentCount); err != nil {
		t.Fatal(err)
	}
	if version != 6 || foreignKeys != 1 || agentCount != 1 {
		t.Fatalf("downgrade version=%d foreign_keys=%d agents=%d", version, foreignKeys, agentCount)
	}
	if _, err := db.ExecContext(ctx, "SELECT updated_at FROM agents"); err == nil {
		t.Fatal("updated_at remained after downgrade")
	}
	if _, err := db.ExecContext(ctx, "SELECT 1 FROM agent_credentials"); err == nil {
		t.Fatal("agent_credentials remained after downgrade")
	}
}

func TestAgentUpdatedAtRejectsNull(t *testing.T) {
	for _, test := range []struct {
		name      string
		statement string
	}{
		{
			name: "explicit null insert",
			statement: `INSERT INTO agents (
				id, location_id, name, credential_hash, credential_generation,
				capabilities_json, created_at, updated_at
			) VALUES ('agent-null', 'location-null', 'agent', X'01', 1, '["http"]', '2026-07-26T00:00:00Z', NULL)`,
		},
		{
			name: "omitted insert",
			statement: `INSERT INTO agents (
				id, location_id, name, credential_hash, credential_generation,
				capabilities_json, created_at
			) VALUES ('agent-null', 'location-null', 'agent', X'01', 1, '["http"]', '2026-07-26T00:00:00Z')`,
		},
		{
			name:      "null update",
			statement: `UPDATE agents SET updated_at = NULL WHERE id = 'agent-valid'`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "updated-at.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			ctx := context.Background()
			if err := sqlitestore.Migrate(ctx, db); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `
				INSERT INTO locations (id, name, enabled, created_at, updated_at)
				VALUES ('location-null', 'null enforcement', 1, '2026-07-26T00:00:00Z', '2026-07-26T00:00:00Z');
				INSERT INTO agents (
					id, location_id, name, credential_hash, credential_generation,
					capabilities_json, created_at, updated_at
				) VALUES (
					'agent-valid', 'location-null', 'valid agent', X'02', 1,
					'["http"]', '2026-07-26T00:00:00Z', '2026-07-26T00:00:00Z'
				);
			`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, test.statement); err == nil {
				t.Fatal("agents.updated_at accepted NULL")
			}
		})
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
