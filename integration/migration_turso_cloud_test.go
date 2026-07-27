package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	migrationcontract "github.com/araihu/xisnove/internal/adapters/migration"
	"github.com/araihu/xisnove/internal/adapters/sqlitecompat"
	"github.com/araihu/xisnove/internal/adapters/tursocloud"
)

func TestMigrationManagedTursoCASLeaseAndProcessFence(t *testing.T) {
	harness := newTursoCloudStorageHarness(t)
	ctx := context.Background()
	leaseOptions := migrationcontract.DefaultOptions("first-managed-migrator")
	leaseOptions.InstallationID = "managed-shared"
	leaseOptions.LeaseTTL = 300 * time.Millisecond
	leaseOptions.PollInterval = 25 * time.Millisecond
	leaseOptions.LockTimeout = time.Second
	_, release, err := sqlitecompat.AcquireDatabaseMigrationLease(ctx, harness.primary.DB, leaseOptions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	blocked := leaseOptions
	blocked.OwnerID = "second-managed-migrator"
	blocked.LockTimeout = 100 * time.Millisecond
	_, _, err = sqlitecompat.AcquireDatabaseMigrationLease(ctx, harness.secondary.DB, blocked)
	if !errors.Is(err, migrationcontract.ErrTimeout) {
		t.Fatalf("managed Turso contender error = %v", err)
	}
	release()
	oldProcess := migrationcontract.ProcessLease{InstallationID: "managed-shared", ProcessID: "pre-M6", ProcessVersion: "pre-M6", Readable: migrationcontract.SchemaInterval{Minimum: 10, Maximum: 10}, TTL: time.Minute}
	if err := tursocloud.AcquireProcessLease(ctx, harness.primary.DB, oldProcess); err != nil {
		t.Fatal(err)
	}
	contractOptions := leaseOptions
	contractOptions.OwnerID = "managed-contract"
	if err := tursocloud.CheckContractWithOptions(ctx, harness.secondary.DB, contractOptions, 11); !errors.Is(err, migrationcontract.ErrLiveIncompatibleProcess) {
		t.Fatalf("managed Turso contract fence error = %v", err)
	}
	if err := tursocloud.ReleaseProcessLease(ctx, harness.secondary.DB, "managed-shared", "pre-M6"); err != nil {
		t.Fatal(err)
	}
	if err := tursocloud.CheckContractWithOptions(ctx, harness.primary.DB, contractOptions, 11); err != nil {
		t.Fatal(err)
	}
}
