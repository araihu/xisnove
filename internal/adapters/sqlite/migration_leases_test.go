package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	migrationcontract "github.com/araihu/xisnove/internal/adapters/migration"
	"github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestProcessLeaseBlocksContractUntilReleaseOrExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "process-leases.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	lease := migrationcontract.ProcessLease{
		InstallationID: "home", ProcessID: "incompatible-server", ProcessVersion: "pre-M6",
		Readable: migrationcontract.SchemaInterval{Minimum: 10, Maximum: 10}, TTL: 150 * time.Millisecond,
	}
	if err := sqlite.AcquireProcessLease(ctx, db, lease); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.CheckContractAllowed(ctx, db, "home", 11); !errors.Is(err, migrationcontract.ErrLiveIncompatibleProcess) {
		t.Fatalf("live lease error = %v", err)
	}
	if err := sqlite.CheckContractAllowed(ctx, db, "other-installation", 11); err != nil {
		t.Fatalf("lease leaked across installations: %v", err)
	}
	if err := sqlite.ReleaseProcessLease(ctx, db, "home", "incompatible-server"); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.CheckContractAllowed(ctx, db, "home", 11); err != nil {
		t.Fatalf("released lease blocked contract: %v", err)
	}
	if err := sqlite.AcquireProcessLease(ctx, db, lease); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	if err := sqlite.CheckContractAllowed(ctx, db, "home", 11); err != nil {
		t.Fatalf("stale lease blocked contract: %v", err)
	}
}

func TestConcurrentSQLiteMigratorsConvergeWithoutInterleaving(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	db1, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db1.Close() })
	db2, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	ctx := context.Background()
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for index, db := range []*sql.DB{db1, db2} {
		wg.Add(1)
		go func(index int, db *sql.DB) {
			defer wg.Done()
			<-start
			options := migrationcontract.DefaultOptions(fmt.Sprintf("writer-%d", index))
			options.LockTimeout = 5 * time.Second
			errs <- sqlite.MigrateWithOptions(ctx, db, options)
		}(index, db)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := sqlite.Ready(ctx, db1); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteMigrationLockTimeoutIsStableAndRetryable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locked.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	lock, err := migrationcontract.AcquireFileLock(context.Background(), path+".xisnove-migrate.lock", time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	options := migrationcontract.DefaultOptions("blocked-writer")
	options.LockTimeout = 80 * time.Millisecond
	options.PollInterval = 10 * time.Millisecond
	err = sqlite.MigrateWithOptions(context.Background(), db, options)
	if !errors.Is(err, migrationcontract.ErrTimeout) || !migrationcontract.Retryable(err) {
		t.Fatalf("lock error = %v, retryable=%v", err, migrationcontract.Retryable(err))
	}
}
