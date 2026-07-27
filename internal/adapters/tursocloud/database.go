package tursocloud

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	application "github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/internal/adapters/migration"
	"github.com/araihu/xisnove/internal/adapters/sqlitecompat"
	_ "github.com/tursodatabase/libsql-client-go/libsql"
)

func Open(ctx context.Context, rawURL, authToken string) (*sql.DB, error) {
	dsn, err := buildDSN(rawURL, authToken)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("libsql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open managed Turso database: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping managed Turso database: %w", err)
	}
	return db, nil
}

func Migrate(ctx context.Context, db *sql.DB) error {
	return migrate(ctx, db)
}

func MigrateWithOptions(ctx context.Context, db *sql.DB, options migration.Options) error {
	return migrateWithOptions(ctx, db, options)
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
	migrationCtx, release, err := sqlitecompat.AcquireDatabaseMigrationLease(ctx, db, options)
	if err != nil {
		return err
	}
	defer release()
	return sqlitecompat.CheckContractAllowed(migrationCtx, db, options.InstallationID, targetSchema)
}

func NewStore(db *sql.DB) application.Store {
	return sqlitecompat.NewStore(db)
}

func buildDSN(rawURL, authToken string) (string, error) {
	if strings.TrimSpace(authToken) == "" {
		return "", fmt.Errorf("managed Turso auth token is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse managed Turso URL: invalid URL")
	}
	if parsed.Scheme != "libsql" || parsed.Host == "" {
		return "", fmt.Errorf("managed Turso URL must use libsql scheme")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("managed Turso URL must not contain user information")
	}
	query := parsed.Query()
	query.Set("authToken", authToken)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
