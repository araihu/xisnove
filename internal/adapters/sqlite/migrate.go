package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	migrations "github.com/araihu/xisnove/db/migrations/sqlite"
	"github.com/pressly/goose/v3"
)

var migrationMu sync.Mutex

const LatestMigrationVersion = 2

func Migrate(ctx context.Context, db *sql.DB) error {
	migrationMu.Lock()
	defer migrationMu.Unlock()

	goose.SetBaseFS(migrations.Files)
	goose.SetTableName("schema_migrations")
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("set migration dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

func Ready(ctx context.Context, db *sql.DB) error {
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	var version int64
	err := db.QueryRowContext(
		ctx,
		`SELECT COALESCE(MAX(version_id), 0)
		 FROM schema_migrations
		 WHERE is_applied = 1`,
	).Scan(&version)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version != LatestMigrationVersion {
		return fmt.Errorf(
			"schema version %d does not match required version %d",
			version,
			LatestMigrationVersion,
		)
	}
	return nil
}
