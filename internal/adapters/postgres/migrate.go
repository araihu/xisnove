package postgres

import (
	"context"
	"database/sql"
	"fmt"

	migrations "github.com/araihu/xisnove/db/migrations/postgres"
	"github.com/pressly/goose/v3"
)

const LatestMigrationVersion = migrations.LatestVersion

func Migrate(ctx context.Context, db *sql.DB) error {
	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		migrations.Files,
		goose.WithTableName("schema_migrations"),
	)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
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
		 WHERE is_applied`,
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
