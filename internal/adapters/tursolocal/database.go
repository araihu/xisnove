package tursolocal

import (
	"context"
	"database/sql"
	"fmt"

	application "github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/internal/adapters/migration"
	"github.com/araihu/xisnove/internal/adapters/sqlitecompat"
)

func Open(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("turso", path)
	if err != nil {
		return nil, fmt.Errorf("open local Turso database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping local Turso database: %w", err)
	}
	return db, nil
}

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

func NewStore(db *sql.DB) application.Store {
	return sqlitecompat.NewStore(db)
}
