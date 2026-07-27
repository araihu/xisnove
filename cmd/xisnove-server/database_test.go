package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/xisnove/internal/adapters/migration"
	"github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestMigrateCommandSeparatesExpandAndContractPhases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "phases.db")
	ctx := context.Background()
	baseArgs := []string{"--database-profile", "sqlite", "--database-url", path, "--installation-id", "home"}
	if err := migrateCommand(ctx, append(baseArgs, "--phase", "expand")); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	lease := migration.ProcessLease{InstallationID: "home", ProcessID: "old", ProcessVersion: "v0.1.0", Readable: migration.SchemaInterval{Minimum: 10, Maximum: 10}, TTL: time.Minute}
	if err := sqlite.AcquireProcessLease(ctx, db, lease); err != nil {
		t.Fatal(err)
	}
	if err := migrateCommand(ctx, append(baseArgs, "--phase", "contract")); !errors.Is(err, migration.ErrLiveIncompatibleProcess) {
		t.Fatalf("contract error = %v", err)
	}
	if err := sqlite.ReleaseProcessLease(ctx, db, "home", "old"); err != nil {
		t.Fatal(err)
	}
	if err := migrateCommand(ctx, append(baseArgs, "--phase", "contract")); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateCommandRejectsInvalidPhaseAndUnboundedTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.db")
	if err := migrateCommand(context.Background(), []string{"--database-url", path, "--phase", "drop"}); err == nil {
		t.Fatal("invalid phase accepted")
	}
	if err := migrateCommand(context.Background(), []string{"--database-url", path, "--lock-timeout", "0s"}); err == nil {
		t.Fatal("zero timeout accepted")
	}
}
