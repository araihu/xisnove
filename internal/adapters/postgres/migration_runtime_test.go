package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	migrationcontract "github.com/araihu/xisnove/internal/adapters/migration"
	"github.com/araihu/xisnove/internal/adapters/postgres"
	postgrescontainer "github.com/araihu/xisnove/internal/testsupport/postgrescontainer"
	"github.com/google/uuid"
)

func TestPostgresReadyAndProcessLeaseFence(t *testing.T) {
	db := openMigrationSchema(t, "runtime")
	ctx := context.Background()
	if err := postgres.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE schema_migrations SET is_applied = false WHERE version_id = $1`, postgres.LatestMigrationVersion); err != nil {
		t.Fatal(err)
	}
	if err := postgres.Ready(ctx, db); err != nil {
		t.Fatalf("minimum supported schema not ready: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE schema_migrations SET is_applied = false WHERE version_id = $1`, postgres.MinimumMigrationVersion); err != nil {
		t.Fatal(err)
	}
	if err := postgres.Ready(ctx, db); !errors.Is(err, migrationcontract.ErrIncompatibleSchema) {
		t.Fatalf("old schema error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE schema_migrations SET is_applied = true WHERE version_id IN ($1, $2)`, postgres.MinimumMigrationVersion, postgres.LatestMigrationVersion); err != nil {
		t.Fatal(err)
	}
	if !postgres.PreviousRuntimeSchemaInterval.Contains(postgres.LatestMigrationVersion) {
		t.Fatal("frozen N-1 runtime cannot read expand schema")
	}
	lease := migrationcontract.ProcessLease{InstallationID: "shared", ProcessID: "pre-M6", ProcessVersion: "pre-M6", Readable: migrationcontract.SchemaInterval{Minimum: 10, Maximum: 10}, TTL: time.Minute}
	if err := postgres.AcquireProcessLease(ctx, db, lease); err != nil {
		t.Fatal(err)
	}
	contractOptions := migrationcontract.DefaultOptions("postgres-contract")
	contractOptions.InstallationID = "shared"
	if err := postgres.CheckContractWithOptions(ctx, db, contractOptions, 11); !errors.Is(err, migrationcontract.ErrLiveIncompatibleProcess) {
		t.Fatalf("contract fence error = %v", err)
	}
	if err := postgres.ReleaseProcessLease(ctx, db, "shared", "pre-M6"); err != nil {
		t.Fatal(err)
	}
	if err := postgres.CheckContractAllowed(ctx, db, "shared", 11); err != nil {
		t.Fatal(err)
	}
	if err := postgres.AcquireProcessLease(ctx, db, lease); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE process_version_leases SET heartbeat_at_ms = -2, expires_at_ms = -1 WHERE installation_id = 'shared' AND process_id = 'pre-M6'`); err != nil {
		t.Fatal(err)
	}
	if err := postgres.CheckContractAllowed(ctx, db, "shared", 11); err != nil {
		t.Fatalf("stale lease blocked contract: %v", err)
	}
}

func TestConcurrentPostgresMigratorsConverge(t *testing.T) {
	db := openMigrationSchema(t, "concurrent")
	ctx := context.Background()
	parsedURL := migrationSchemaURL(t, db)
	second, err := postgres.Open(ctx, parsedURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for index, candidate := range []*sql.DB{db, second} {
		wg.Add(1)
		go func(index int, candidate *sql.DB) {
			defer wg.Done()
			<-start
			options := migrationcontract.DefaultOptions(fmt.Sprintf("postgres-%d", index))
			errs <- postgres.MigrateWithOptions(ctx, candidate, options)
		}(index, candidate)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := postgres.Ready(ctx, db); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresMigrationAdvisoryLockTimeoutIsStable(t *testing.T) {
	db := openMigrationSchema(t, "timeout")
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	const migrationLockID int64 = 0x7869736e6f7665
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockID)
	})
	second, err := postgres.Open(ctx, migrationSchemaURL(t, db))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	options := migrationcontract.DefaultOptions("blocked")
	options.LockTimeout = 80 * time.Millisecond
	options.PollInterval = 10 * time.Millisecond
	err = postgres.MigrateWithOptions(ctx, second, options)
	if !errors.Is(err, migrationcontract.ErrTimeout) || !migrationcontract.Retryable(err) {
		t.Fatalf("advisory lock error = %v", err)
	}
}

func TestPostgresProcessLeaseCannotEnterExclusiveMigrationFence(t *testing.T) {
	db := openMigrationSchema(t, "process_fence")
	ctx := context.Background()
	if err := postgres.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	const migrationLockID int64 = 0x7869736e6f7665
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockID)
	}()
	lease := migrationcontract.ProcessLease{
		InstallationID: "home", ProcessID: "late-old", ProcessVersion: "0.0.1",
		Readable: migrationcontract.SchemaInterval{Minimum: 10, Maximum: 10}, TTL: time.Minute,
	}
	if err := postgres.AcquireProcessLease(ctx, db, lease); !errors.Is(err, migrationcontract.ErrContention) {
		t.Fatalf("AcquireProcessLease() error = %v, want migration contention", err)
	}
}

func openMigrationSchema(t *testing.T, prefix string) *sql.DB {
	t.Helper()
	baseURL := postgrescontainer.URL(t, os.Getenv("XISNOVE_TEST_POSTGRES_URL"))
	ctx := context.Background()
	admin, err := postgres.Open(ctx, baseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "xisnove_" + prefix + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := postgres.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migrationURLs.Store(db, parsed.String())
	return db
}

var migrationURLs sync.Map

func migrationSchemaURL(t *testing.T, db *sql.DB) string {
	t.Helper()
	value, ok := migrationURLs.Load(db)
	if !ok {
		t.Fatal("migration schema URL not registered")
	}
	return value.(string)
}
