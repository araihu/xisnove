package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	migrations "github.com/araihu/xisnove/db/migrations/postgres"
	migrationcontract "github.com/araihu/xisnove/internal/adapters/migration"
	"github.com/pressly/goose/v3"
	gooselock "github.com/pressly/goose/v3/lock"
)

const LatestMigrationVersion = migrations.LatestVersion
const MinimumMigrationVersion = LatestMigrationVersion - 1
const migrationAdvisoryLockID int64 = 0x7869736e6f7665

var SupportedSchemaInterval = migrationcontract.SchemaInterval{Minimum: MinimumMigrationVersion, Maximum: LatestMigrationVersion}

// PreviousRuntimeSchemaInterval freezes M6.1 for the next release transition;
// pre-M6.1 binaries accepted only exact schema 10.
var PreviousRuntimeSchemaInterval = migrationcontract.SchemaInterval{Minimum: MinimumMigrationVersion, Maximum: LatestMigrationVersion}
var SchemaMigrationPlan = migrationcontract.PhasePlan{ExpandThrough: LatestMigrationVersion, ContractThrough: LatestMigrationVersion}

func Migrate(ctx context.Context, db *sql.DB) error {
	return MigrateWithOptions(ctx, db, migrationcontract.DefaultOptions(randomMigrationOwnerID()))
}

func MigrateWithOptions(ctx context.Context, db *sql.DB, options migrationcontract.Options) error {
	if err := options.Validate(); err != nil {
		return err
	}
	if err := SchemaMigrationPlan.Validate(migrations.LatestVersion); err != nil {
		return err
	}
	return withMigrationLock(ctx, db, options, func(lockCtx context.Context) error {
		return migrateTo(lockCtx, db, SchemaMigrationPlan.Target(migrationcontract.PhaseExpand))
	})
}

func withMigrationLock(ctx context.Context, db *sql.DB, options migrationcontract.Options, action func(context.Context) error) error {
	locker := &advisoryLocker{lockID: migrationAdvisoryLockID, poll: options.PollInterval}
	lockCtx, cancel := context.WithTimeout(ctx, options.LockTimeout)
	conn, err := db.Conn(lockCtx)
	if err != nil {
		cancel()
		return migrationcontract.ClassifyLockError(err)
	}
	if err := locker.SessionLock(lockCtx, conn); err != nil {
		cancel()
		_ = conn.Close()
		if errors.Is(err, context.DeadlineExceeded) {
			return migrationcontract.NewTimeoutError("PostgreSQL advisory lock deadline exceeded")
		}
		return err
	}
	cancel()
	defer conn.Close()
	defer func() { _ = locker.SessionUnlock(context.Background(), conn) }()
	return action(ctx)
}

func migrateTo(ctx context.Context, db *sql.DB, target int64) error {
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.Files, goose.WithTableName("schema_migrations"))
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	if _, err := provider.UpTo(ctx, target); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

type advisoryLocker struct {
	lockID int64
	poll   time.Duration
}

var _ gooselock.SessionLocker = (*advisoryLocker)(nil)

func (l *advisoryLocker) SessionLock(ctx context.Context, conn *sql.Conn) error {
	for {
		var acquired bool
		if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", l.lockID).Scan(&acquired); err != nil {
			return fmt.Errorf("try PostgreSQL migration advisory lock: %w", err)
		}
		if acquired {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(l.poll):
		}
	}
}

func (l *advisoryLocker) SessionUnlock(ctx context.Context, conn *sql.Conn) error {
	// Cleanup must not inherit an already-expired lock-acquisition deadline.
	unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var unlocked bool
	if err := conn.QueryRowContext(unlockCtx, "SELECT pg_advisory_unlock($1)", l.lockID).Scan(&unlocked); err != nil {
		return fmt.Errorf("unlock PostgreSQL migration advisory lock: %w", err)
	}
	if !unlocked {
		return fmt.Errorf("PostgreSQL migration advisory lock was not held")
	}
	return nil
}

func Ready(ctx context.Context, db *sql.DB) error {
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	var version int64
	err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version_id), 0) FROM schema_migrations WHERE is_applied`).Scan(&version)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if !SupportedSchemaInterval.Contains(version) {
		return migrationcontract.NewIncompatibleSchemaError(fmt.Sprintf("schema version %d is outside supported interval [%d,%d]", version, SupportedSchemaInterval.Minimum, SupportedSchemaInterval.Maximum))
	}
	return nil
}

const postgresNowMillis = `floor(extract(epoch from clock_timestamp()) * 1000)::bigint`

func AcquireProcessLease(ctx context.Context, db *sql.DB, lease migrationcontract.ProcessLease) error {
	if err := lease.Validate(); err != nil {
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire process lease connection: %w", err)
	}
	defer conn.Close()
	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock_shared($1)", migrationAdvisoryLockID).Scan(&acquired); err != nil {
		return fmt.Errorf("enter process version lease fence: %w", err)
	}
	if !acquired {
		return migrationcontract.NewContentionError("migration fence is active")
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock_shared($1)", migrationAdvisoryLockID)
	}()
	_, err = conn.ExecContext(ctx, `INSERT INTO process_version_leases (
		installation_id, process_id, process_version, minimum_schema_version,
		maximum_schema_version, heartbeat_at_ms, expires_at_ms
	) VALUES ($1, $2, $3, $4, $5, `+postgresNowMillis+`, `+postgresNowMillis+` + $6)
	ON CONFLICT (installation_id, process_id) DO UPDATE SET
		process_version = excluded.process_version,
		minimum_schema_version = excluded.minimum_schema_version,
		maximum_schema_version = excluded.maximum_schema_version,
		heartbeat_at_ms = excluded.heartbeat_at_ms,
		expires_at_ms = excluded.expires_at_ms`,
		lease.InstallationID, lease.ProcessID, lease.ProcessVersion,
		lease.Readable.Minimum, lease.Readable.Maximum, lease.TTL.Milliseconds())
	if err != nil {
		return fmt.Errorf("acquire process version lease: %w", err)
	}
	return nil
}

func RenewProcessLease(ctx context.Context, db *sql.DB, lease migrationcontract.ProcessLease) error {
	if err := lease.Validate(); err != nil {
		return err
	}
	result, err := db.ExecContext(ctx, `UPDATE process_version_leases SET
		process_version = $1, minimum_schema_version = $2, maximum_schema_version = $3,
		heartbeat_at_ms = `+postgresNowMillis+`, expires_at_ms = `+postgresNowMillis+` + $4
		WHERE installation_id = $5 AND process_id = $6`,
		lease.ProcessVersion, lease.Readable.Minimum, lease.Readable.Maximum,
		lease.TTL.Milliseconds(), lease.InstallationID, lease.ProcessID)
	if err != nil {
		return fmt.Errorf("renew process version lease: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("inspect process version lease renewal: %w", err)
	} else if affected != 1 {
		return fmt.Errorf("renew process version lease: lease is absent")
	}
	return nil
}

func ReleaseProcessLease(ctx context.Context, db *sql.DB, installationID, processID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM process_version_leases WHERE installation_id = $1 AND process_id = $2`, installationID, processID)
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
	if _, err := db.ExecContext(ctx, `DELETE FROM process_version_leases WHERE installation_id = $1 AND expires_at_ms <= `+postgresNowMillis, installationID); err != nil {
		return fmt.Errorf("expire stale process version leases: %w", err)
	}
	var incompatible int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM process_version_leases
		WHERE installation_id = $1 AND expires_at_ms > `+postgresNowMillis+`
		AND (minimum_schema_version > $2 OR maximum_schema_version < $2)`, installationID, targetSchema).Scan(&incompatible)
	if err != nil {
		return fmt.Errorf("check process version leases: %w", err)
	}
	if incompatible > 0 {
		return migrationcontract.NewLiveIncompatibleProcessError(fmt.Sprintf("%d live process lease(s) cannot read schema %d", incompatible, targetSchema))
	}
	return nil
}

func CheckContractWithOptions(ctx context.Context, db *sql.DB, options migrationcontract.Options, targetSchema int64) error {
	if err := options.Validate(); err != nil {
		return err
	}
	return withMigrationLock(ctx, db, options, func(lockCtx context.Context) error {
		return CheckContractAllowed(lockCtx, db, options.InstallationID, targetSchema)
	})
}

func ContractWithOptions(ctx context.Context, db *sql.DB, options migrationcontract.Options) error {
	if err := options.Validate(); err != nil {
		return err
	}
	if err := SchemaMigrationPlan.Validate(migrations.LatestVersion); err != nil {
		return err
	}
	target := SchemaMigrationPlan.Target(migrationcontract.PhaseContract)
	return withMigrationLock(ctx, db, options, func(lockCtx context.Context) error {
		if err := CheckContractAllowed(lockCtx, db, options.InstallationID, target); err != nil {
			return err
		}
		return migrateTo(lockCtx, db, target)
	})
}

func randomMigrationOwnerID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("migration-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}
