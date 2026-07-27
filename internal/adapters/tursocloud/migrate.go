package tursocloud

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	migrations "github.com/araihu/xisnove/db/migrations/sqlite"
	"github.com/araihu/xisnove/internal/adapters/migration"
	"github.com/araihu/xisnove/internal/adapters/sqlitecompat"
)

const initializeVersionTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	version_id INTEGER NOT NULL,
	is_applied INTEGER NOT NULL,
	tstamp TIMESTAMP DEFAULT (datetime('now'))
);
INSERT INTO schema_migrations (version_id, is_applied)
SELECT 0, 1
WHERE NOT EXISTS (
	SELECT 1 FROM schema_migrations WHERE version_id = 0
);`

// migrate applies the shared SQLite-compatible migrations without pinning a
// database/sql connection between requests. The managed libSQL HTTP transport
// closes a stream after each request, while Goose's provider deliberately pins
// one connection for its entire run. Sending each migration as one libSQL batch
// keeps that migration and its version record atomic.
func migrate(ctx context.Context, db *sql.DB) error {
	return migrateWithOptions(ctx, db, migration.DefaultOptions(fmt.Sprintf("managed-turso-%d", time.Now().UnixNano())))
}

func migrateWithOptions(ctx context.Context, db *sql.DB, options migration.Options) error {
	if err := sqlitecompat.SchemaMigrationPlan.Validate(migrations.LatestVersion); err != nil {
		return err
	}
	migrationCtx, release, err := sqlitecompat.AcquireDatabaseMigrationLease(ctx, db, options)
	if err != nil {
		return err
	}
	defer release()
	return migrateUnlocked(migrationCtx, db, sqlitecompat.SchemaMigrationPlan.Target(migration.PhaseExpand))
}

func migrateUnlocked(ctx context.Context, db *sql.DB, target int64) error {
	if _, err := db.ExecContext(ctx, initializeVersionTable); err != nil {
		return fmt.Errorf("initialize managed Turso migration table: %w", err)
	}

	var current int64
	if err := db.QueryRowContext(
		ctx,
		`SELECT COALESCE(MAX(version_id), 0)
		 FROM schema_migrations
		 WHERE is_applied = 1`,
	).Scan(&current); err != nil {
		return fmt.Errorf("read managed Turso migration version: %w", err)
	}

	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		return fmt.Errorf("list managed Turso migrations: %w", err)
	}
	sort.Strings(names)
	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			return err
		}
		if version <= current {
			continue
		}
		if version > target {
			break
		}
		content, err := migrations.Files.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read managed Turso migration %s: %w", name, err)
		}
		up, err := upMigration(string(content))
		if err != nil {
			return fmt.Errorf("parse managed Turso migration %s: %w", name, err)
		}
		batch := up + fmt.Sprintf(
			"\nINSERT INTO schema_migrations (version_id, is_applied) VALUES (%d, 1);",
			version,
		)
		if _, err := db.ExecContext(ctx, batch); err != nil {
			return fmt.Errorf(
				"apply managed Turso migration %s: %w",
				name,
				err,
			)
		}
		current = version
	}
	return nil
}

func migrationVersion(name string) (int64, error) {
	base := filepath.Base(name)
	prefix, _, ok := strings.Cut(base, "_")
	if !ok {
		return 0, fmt.Errorf("managed Turso migration has invalid name %q", base)
	}
	version, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("managed Turso migration has invalid version %q", base)
	}
	return version, nil
}

func upMigration(content string) (string, error) {
	const (
		upMarker   = "-- +goose Up"
		downMarker = "-- +goose Down"
	)
	start := strings.Index(content, upMarker)
	if start < 0 {
		return "", fmt.Errorf("missing %q marker", upMarker)
	}
	up := content[start+len(upMarker):]
	if end := strings.Index(up, downMarker); end >= 0 {
		up = up[:end]
	}
	up = strings.TrimSpace(up)
	if up == "" {
		return "", fmt.Errorf("empty up migration")
	}
	return up, nil
}
