package integration_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	postgresmigrations "github.com/araihu/xisnove/db/migrations/postgres"
	sqlitemigrations "github.com/araihu/xisnove/db/migrations/sqlite"
	"github.com/araihu/xisnove/internal/adapters/database"
	postgresstore "github.com/araihu/xisnove/internal/adapters/postgres"
	"github.com/pressly/goose/v3"
)

func TestMigrationUpgradeAcrossLocalProfiles(t *testing.T) {
	t.Parallel()

	for _, profile := range []database.Profile{
		database.ProfileSQLite,
		database.ProfileTursoLocal,
	} {
		profile := profile
		t.Run(string(profile), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			handle, err := database.Open(ctx, database.Config{
				Profile: profile,
				URL:     filepath.Join(t.TempDir(), "upgrade.db"),
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = handle.Close() })
			provider, err := goose.NewProvider(
				goose.DialectSQLite3,
				handle.DB,
				sqlitemigrations.Files,
				goose.WithTableName("schema_migrations"),
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.UpTo(ctx, 1); err != nil {
				t.Fatal(err)
			}
			seedVersionOneMonitor(t, handle, false)
			if err := handle.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			assertUpgradedMonitor(t, handle)
		})
	}
}

func TestMigrationUpgradePostgres(t *testing.T) {
	baseURL := os.Getenv("XISNOVE_TEST_POSTGRES_URL")
	if baseURL == "" {
		t.Skip("XISNOVE_TEST_POSTGRES_URL is not set")
	}
	ctx := context.Background()
	admin, err := postgresstore.Open(ctx, baseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "xisnove_upgrade_" + randomHex(t)
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	handle, err := database.Open(ctx, database.Config{
		Profile: database.ProfilePostgres,
		URL:     parsed.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		handle.DB,
		postgresmigrations.Files,
		goose.WithTableName("schema_migrations"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	seedVersionOneMonitor(t, handle, true)
	if err := handle.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	assertUpgradedMonitor(t, handle)
}

func seedVersionOneMonitor(t *testing.T, handle *database.Handle, postgres bool) {
	t.Helper()
	locationID := "00000000-0000-4000-8000-000000000101"
	monitorID := "00000000-0000-4000-8000-000000000102"
	if _, err := handle.DB.ExecContext(context.Background(), `
		INSERT INTO locations (id, name, created_at)
		VALUES ('`+locationID+`', 'upgrade-location', '2026-07-25T12:00:00Z')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.DB.ExecContext(context.Background(), `
		INSERT INTO monitors (
			id, name, kind, interval_ms, timeout_ms, failure_threshold,
			recovery_threshold, http_json, enabled, next_run_at, created_at, updated_at
		) VALUES (
			'`+monitorID+`', 'upgrade-monitor', 'http', 60000, 5000, 3, 2,
			'{"Method":"GET","URL":"https://example.com"}',
			`+booleanLiteral(postgres)+`, '2026-07-25T12:00:00Z', '2026-07-25T12:00:00Z', '2026-07-25T12:00:00Z'
		)
	`); err != nil {
		t.Fatal(err)
	}
}

func assertUpgradedMonitor(t *testing.T, handle *database.Handle) {
	t.Helper()
	if err := handle.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	var kind string
	var probe []byte
	if err := handle.DB.QueryRowContext(
		context.Background(),
		"SELECT kind, probe_json FROM monitors WHERE name = 'upgrade-monitor'",
	).Scan(&kind, &probe); err != nil {
		t.Fatal(err)
	}
	if kind != "http" || len(probe) == 0 {
		t.Fatalf("upgraded monitor kind=%q probe=%s", kind, probe)
	}
}

func booleanLiteral(postgres bool) string {
	if postgres {
		return "TRUE"
	}
	return "1"
}

func randomHex(t *testing.T) string {
	t.Helper()
	value := make([]byte, 6)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(fmt.Errorf("generate schema suffix: %w", err))
	}
	return hex.EncodeToString(value)
}
