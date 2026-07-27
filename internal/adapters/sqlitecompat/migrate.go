package sqlitecompat

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	migrations "github.com/araihu/xisnove/db/migrations/sqlite"
	migrationcontract "github.com/araihu/xisnove/internal/adapters/migration"
	"github.com/pressly/goose/v3"
)

const LatestMigrationVersion = migrations.LatestVersion
const MinimumMigrationVersion = LatestMigrationVersion - 1

var SupportedSchemaInterval = migrationcontract.SchemaInterval{
	Minimum: MinimumMigrationVersion,
	Maximum: LatestMigrationVersion,
}

// PreviousRuntimeSchemaInterval is the frozen N-1 fixture. Migration 11 is an
// additive expand migration, so both N-1 and N declare it readable.
var PreviousRuntimeSchemaInterval = migrationcontract.SchemaInterval{
	Minimum: MinimumMigrationVersion,
	Maximum: LatestMigrationVersion,
}

func Migrate(ctx context.Context, db *sql.DB) error {
	return MigrateWithOptions(ctx, db, migrationcontract.DefaultOptions(randomOwnerID()))
}

func MigrateWithOptions(ctx context.Context, db *sql.DB, options migrationcontract.Options) error {
	if err := options.Validate(); err != nil {
		return err
	}
	migrationCtx, release, err := acquireMigrationLock(ctx, db, options)
	if err != nil {
		return err
	}
	defer release()
	return migrateUnlocked(migrationCtx, db)
}

func migrateUnlocked(ctx context.Context, db *sql.DB) error {
	provider, err := goose.NewProvider(
		goose.DialectSQLite3,
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
		 WHERE is_applied = 1`,
	).Scan(&version)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if !SupportedSchemaInterval.Contains(version) {
		return migrationcontract.NewIncompatibleSchemaError(fmt.Sprintf(
			"schema version %d is outside supported interval [%d,%d]",
			version, SupportedSchemaInterval.Minimum, SupportedSchemaInterval.Maximum,
		))
	}
	return nil
}

func databaseFile(ctx context.Context, db *sql.DB) string {
	rows, err := db.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return ""
	}
	defer rows.Close()
	for rows.Next() {
		var sequence int
		var name, path string
		if rows.Scan(&sequence, &name, &path) == nil && name == "main" {
			return path
		}
	}
	return ""
}

func acquireMigrationLock(ctx context.Context, db *sql.DB, options migrationcontract.Options) (context.Context, func(), error) {
	if path := databaseFile(ctx, db); path != "" {
		lock, err := migrationcontract.AcquireFileLock(ctx, path+".xisnove-migrate.lock", options.LockTimeout, options.PollInterval)
		if err != nil {
			return nil, nil, err
		}
		return ctx, func() { _ = lock.Close() }, nil
	}
	return acquireDatabaseLease(ctx, db, options)
}

const initializeMigrationLease = `CREATE TABLE IF NOT EXISTS migration_leases (
	installation_id TEXT PRIMARY KEY,
	owner_id TEXT NOT NULL,
	heartbeat_at_ms INTEGER NOT NULL,
	expires_at_ms INTEGER NOT NULL CHECK (expires_at_ms > heartbeat_at_ms)
)`

const sqliteNowMillis = `CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER)`

func acquireDatabaseLease(ctx context.Context, db *sql.DB, options migrationcontract.Options) (context.Context, func(), error) {
	if _, err := db.ExecContext(ctx, initializeMigrationLease); err != nil {
		return nil, nil, fmt.Errorf("initialize migration lease: %w", err)
	}
	lockCtx, cancel := context.WithTimeout(ctx, options.LockTimeout)
	defer cancel()
	ttlMillis := options.LeaseTTL.Milliseconds()
	for {
		result, err := db.ExecContext(lockCtx, `
			INSERT INTO migration_leases (installation_id, owner_id, heartbeat_at_ms, expires_at_ms)
			VALUES (?, ?, `+sqliteNowMillis+`, `+sqliteNowMillis+` + ?)
			ON CONFLICT (installation_id) DO UPDATE SET
				owner_id = excluded.owner_id,
				heartbeat_at_ms = excluded.heartbeat_at_ms,
				expires_at_ms = excluded.expires_at_ms
			WHERE migration_leases.owner_id = excluded.owner_id
			   OR migration_leases.expires_at_ms <= `+sqliteNowMillis,
			options.InstallationID, options.OwnerID, ttlMillis,
		)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, nil, migrationcontract.NewTimeoutError("database migration lease deadline exceeded")
			}
			return nil, nil, fmt.Errorf("acquire migration lease: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected == 1 {
			leaseCtx, cancelLease := context.WithCancel(ctx)
			done := make(chan struct{})
			go heartbeatMigrationLease(leaseCtx, db, options, ttlMillis, cancelLease, done)
			return leaseCtx, func() {
				cancelLease()
				<-done
				releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _ = db.ExecContext(releaseCtx, `DELETE FROM migration_leases WHERE installation_id = ? AND owner_id = ?`, options.InstallationID, options.OwnerID)
			}, nil
		}
		select {
		case <-lockCtx.Done():
			return nil, nil, migrationcontract.ClassifyLockError(lockCtx.Err())
		case <-time.After(options.PollInterval):
		}
	}
}

func heartbeatMigrationLease(ctx context.Context, db *sql.DB, options migrationcontract.Options, ttlMillis int64, cancel context.CancelFunc, done chan<- struct{}) {
	defer close(done)
	interval := options.LeaseTTL / 3
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			result, err := db.ExecContext(ctx, `UPDATE migration_leases SET heartbeat_at_ms = `+sqliteNowMillis+`, expires_at_ms = `+sqliteNowMillis+` + ? WHERE installation_id = ? AND owner_id = ?`, ttlMillis, options.InstallationID, options.OwnerID)
			if err != nil {
				cancel()
				return
			}
			affected, err := result.RowsAffected()
			if err != nil || affected != 1 {
				cancel()
				return
			}
		}
	}
}

// AcquireDatabaseMigrationLease exposes the database-backed CAS fallback used
// by managed libSQL, where no local database file exists to lock.
func AcquireDatabaseMigrationLease(ctx context.Context, db *sql.DB, options migrationcontract.Options) (context.Context, func(), error) {
	if err := options.Validate(); err != nil {
		return nil, nil, err
	}
	return acquireDatabaseLease(ctx, db, options)
}

func AcquireProcessLease(ctx context.Context, db *sql.DB, lease migrationcontract.ProcessLease) error {
	if err := lease.Validate(); err != nil {
		return err
	}
	ttlMillis := lease.TTL.Milliseconds()
	_, err := db.ExecContext(ctx, `
		INSERT INTO process_version_leases (
			installation_id, process_id, process_version, minimum_schema_version,
			maximum_schema_version, heartbeat_at_ms, expires_at_ms
		) VALUES (?, ?, ?, ?, ?, `+sqliteNowMillis+`, `+sqliteNowMillis+` + ?)
		ON CONFLICT (installation_id, process_id) DO UPDATE SET
			process_version = excluded.process_version,
			minimum_schema_version = excluded.minimum_schema_version,
			maximum_schema_version = excluded.maximum_schema_version,
			heartbeat_at_ms = excluded.heartbeat_at_ms,
			expires_at_ms = excluded.expires_at_ms`,
		lease.InstallationID, lease.ProcessID, lease.ProcessVersion,
		lease.Readable.Minimum, lease.Readable.Maximum, ttlMillis,
	)
	if err != nil {
		return fmt.Errorf("acquire process version lease: %w", err)
	}
	return nil
}

func ReleaseProcessLease(ctx context.Context, db *sql.DB, installationID, processID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM process_version_leases WHERE installation_id = ? AND process_id = ?`, installationID, processID)
	if err != nil {
		return fmt.Errorf("release process version lease: %w", err)
	}
	return nil
}

func CheckContractAllowed(ctx context.Context, db *sql.DB, installationID string, targetSchema int64) error {
	if installationID == "" {
		return fmt.Errorf("contract installation ID is required")
	}
	if targetSchema <= 0 {
		return fmt.Errorf("contract target schema must be positive")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM process_version_leases WHERE installation_id = ? AND expires_at_ms <= `+sqliteNowMillis, installationID); err != nil {
		return fmt.Errorf("expire stale process version leases: %w", err)
	}
	var incompatible int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM process_version_leases
		WHERE installation_id = ? AND expires_at_ms > `+sqliteNowMillis+`
		AND (minimum_schema_version > ? OR maximum_schema_version < ?)`, installationID, targetSchema, targetSchema).Scan(&incompatible)
	if err != nil {
		return fmt.Errorf("check process version leases: %w", err)
	}
	if incompatible > 0 {
		return migrationcontract.NewLiveIncompatibleProcessError(fmt.Sprintf("%d live process lease(s) cannot read schema %d", incompatible, targetSchema))
	}
	return nil
}

func CheckContractWithOptions(ctx context.Context, db *sql.DB, options migrationcontract.Options, targetSchema int64) error {
	migrationCtx, release, err := acquireMigrationLock(ctx, db, options)
	if err != nil {
		return err
	}
	defer release()
	return CheckContractAllowed(migrationCtx, db, options.InstallationID, targetSchema)
}

func randomOwnerID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("migration-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}
