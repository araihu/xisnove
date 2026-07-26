package postgres_test

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	migrations "github.com/araihu/xisnove/db/migrations/postgres"
	"github.com/araihu/xisnove/internal/adapters/postgres"
	postgrescontainer "github.com/araihu/xisnove/internal/testsupport/postgrescontainer"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestLocationLifecycleMigrationUpgradesVersionSevenAndDowngrades(t *testing.T) {
	baseURL := postgrescontainer.URL(t, os.Getenv("XISNOVE_TEST_POSTGRES_URL"))
	ctx := context.Background()
	admin, err := postgres.Open(ctx, baseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "xisnove_location_v8_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.Files, goose.WithTableName("schema_migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 7); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 7, 26, 4, 0, 0, 0, time.UTC)
	if _, err := db.ExecContext(ctx, `INSERT INTO locations (id, name, created_at) VALUES ($1, $2, $3)`, "00000000-0000-4000-8000-000000000800", "upgrade-v8", createdAt); err != nil {
		t.Fatal(err)
	}
	if err := postgres.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	var enabled bool
	var updatedAt time.Time
	if err := db.QueryRowContext(ctx, `SELECT enabled, updated_at FROM locations WHERE id = $1`, "00000000-0000-4000-8000-000000000800").Scan(&enabled, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if !enabled || !updatedAt.Equal(createdAt) {
		t.Fatalf("upgraded location enabled=%v updated_at=%s", enabled, updatedAt)
	}
	if _, err := provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "SELECT enabled, updated_at FROM locations"); err == nil {
		t.Fatal("location lifecycle columns remained after downgrade")
	}
}
