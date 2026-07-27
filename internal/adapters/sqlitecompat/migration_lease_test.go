package sqlitecompat

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	migrationcontract "github.com/araihu/xisnove/internal/adapters/migration"
	_ "modernc.org/sqlite"
)

func TestDatabaseMigrationLeaseHeartbeatsAndSerializes(t *testing.T) {
	dsn := "file:migration-lease?mode=memory&cache=shared"
	first, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	ctx := context.Background()
	options := migrationcontract.DefaultOptions("first")
	options.LeaseTTL = 90 * time.Millisecond
	options.PollInterval = 10 * time.Millisecond
	options.LockTimeout = 300 * time.Millisecond
	_, release, err := AcquireDatabaseMigrationLease(ctx, first, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	time.Sleep(180 * time.Millisecond)
	blocked := options
	blocked.OwnerID = "second"
	blocked.LockTimeout = 60 * time.Millisecond
	_, _, err = AcquireDatabaseMigrationLease(ctx, second, blocked)
	if !errors.Is(err, migrationcontract.ErrTimeout) {
		t.Fatalf("contender error = %v", err)
	}
	release()
	_, releaseSecond, err := AcquireDatabaseMigrationLease(ctx, second, blocked)
	if err != nil {
		t.Fatal(err)
	}
	releaseSecond()
}

func TestProcessLeaseCannotEnterWhileMigrationLeaseIsHeld(t *testing.T) {
	dsn := "file:process-fence?mode=memory&cache=shared"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	options := migrationcontract.DefaultOptions("contract")
	options.InstallationID = "home"
	_, release, err := AcquireDatabaseMigrationLease(ctx, db, options)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	lease := migrationcontract.ProcessLease{
		InstallationID: "home", ProcessID: "late-old", ProcessVersion: "0.0.1",
		Readable: migrationcontract.SchemaInterval{Minimum: 10, Maximum: 10}, TTL: time.Minute,
	}
	if err := AcquireProcessLease(ctx, db, lease); !errors.Is(err, migrationcontract.ErrContention) {
		t.Fatalf("AcquireProcessLease() error = %v, want migration contention", err)
	}
}
