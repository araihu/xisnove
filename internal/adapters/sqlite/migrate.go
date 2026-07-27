package sqlite

import (
	"context"
	"database/sql"

	"github.com/araihu/xisnove/internal/adapters/migration"
	"github.com/araihu/xisnove/internal/adapters/sqlitecompat"
)

const LatestMigrationVersion = sqlitecompat.LatestMigrationVersion
const MinimumMigrationVersion = sqlitecompat.MinimumMigrationVersion

func Migrate(ctx context.Context, db *sql.DB) error {
	return sqlitecompat.Migrate(ctx, db)
}

func MigrateWithOptions(ctx context.Context, db *sql.DB, options migration.Options) error {
	return sqlitecompat.MigrateWithOptions(ctx, db, options)
}

func Ready(ctx context.Context, db *sql.DB) error {
	return sqlitecompat.Ready(ctx, db)
}

func AcquireProcessLease(ctx context.Context, db *sql.DB, lease migration.ProcessLease) error {
	return sqlitecompat.AcquireProcessLease(ctx, db, lease)
}

func ReleaseProcessLease(ctx context.Context, db *sql.DB, installationID, processID string) error {
	return sqlitecompat.ReleaseProcessLease(ctx, db, installationID, processID)
}

func CheckContractAllowed(ctx context.Context, db *sql.DB, installationID string, targetSchema int64) error {
	return sqlitecompat.CheckContractAllowed(ctx, db, installationID, targetSchema)
}

func CheckContractWithOptions(ctx context.Context, db *sql.DB, options migration.Options, targetSchema int64) error {
	return sqlitecompat.CheckContractWithOptions(ctx, db, options, targetSchema)
}
