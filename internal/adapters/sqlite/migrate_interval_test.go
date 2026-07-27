package sqlite_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/araihu/xisnove/internal/adapters/sqlite"
	"github.com/araihu/xisnove/internal/adapters/sqlitecompat"
)

func TestReadyAcceptsSupportedSchemaInterval(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "interval.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Ready(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE schema_migrations SET is_applied = 0 WHERE version_id = ?", sqlite.LatestMigrationVersion); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.Ready(ctx, db); err != nil {
		t.Fatalf("N-1 schema not ready: %v", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE schema_migrations SET is_applied = 0 WHERE version_id = ?", sqlite.MinimumMigrationVersion); err != nil {
		t.Fatal(err)
	}
	err = sqlite.Ready(ctx, db)
	if err == nil || !strings.Contains(err.Error(), "supported interval") {
		t.Fatalf("older schema error = %v", err)
	}
}

func TestExpandMigrationRemainsReadableByFrozenPreviousAndCurrentRuntime(t *testing.T) {
	if !sqlitecompat.PreviousRuntimeSchemaInterval.Contains(sqlite.LatestMigrationVersion) {
		t.Fatal("frozen N-1 runtime cannot read expand schema")
	}
	if !sqlitecompat.SupportedSchemaInterval.Contains(sqlite.LatestMigrationVersion) {
		t.Fatal("current runtime cannot read expand schema")
	}
}
