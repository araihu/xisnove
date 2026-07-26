package integration_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/xisnove/internal/adapters/database"
	postgresstore "github.com/araihu/xisnove/internal/adapters/postgres"
	postgrescontainer "github.com/araihu/xisnove/internal/testsupport/postgrescontainer"
	tursotest "github.com/araihu/xisnove/internal/testsupport/tursocloud"
)

type storageHarness struct {
	primary   *database.Handle
	secondary *database.Handle
	config    database.Config
}

func TestStorageMatrix(t *testing.T) {
	t.Run("SQLite", func(t *testing.T) {
		runStorageJourney(t, newFileStorageHarness(t, database.ProfileSQLite))
	})
	t.Run("TursoLocal", func(t *testing.T) {
		runStorageJourney(t, newFileStorageHarness(t, database.ProfileTursoLocal))
	})
	t.Run("Postgres", func(t *testing.T) {
		runStorageJourney(t, newPostgresStorageHarness(t))
	})
	t.Run("TursoCloud", func(t *testing.T) {
		runStorageJourney(t, newTursoCloudStorageHarness(t))
	})
}

func newFileStorageHarness(t *testing.T, profile database.Profile) *storageHarness {
	t.Helper()
	return openStorageHarness(t, database.Config{
		Profile: profile,
		URL:     filepath.Join(t.TempDir(), "storage-matrix.db"),
	})
}

func newPostgresStorageHarness(t *testing.T) *storageHarness {
	t.Helper()
	baseURL := postgrescontainer.URL(t, os.Getenv("XISNOVE_TEST_POSTGRES_URL"))
	ctx := context.Background()
	admin, err := postgresstore.Open(ctx, baseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "xisnove_matrix_" + randomHex(t)
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop PostgreSQL storage-matrix schema: %v", err)
		}
	})
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return openStorageHarness(t, database.Config{
		Profile: database.ProfilePostgres,
		URL:     parsed.String(),
	})
}

func newTursoCloudStorageHarness(t *testing.T) *storageHarness {
	t.Helper()
	rawURL := os.Getenv("XISNOVE_TEST_TURSO_URL")
	authToken := os.Getenv("XISNOVE_TEST_TURSO_TOKEN")
	if rawURL != "" || authToken != "" {
		if rawURL == "" || authToken == "" {
			t.Fatal("XISNOVE_TEST_TURSO_URL and XISNOVE_TEST_TURSO_TOKEN must be set together")
		}
		if os.Getenv("XISNOVE_TEST_TURSO_ALLOW_RESET") != "1" {
			t.Fatal("XISNOVE_TEST_TURSO_ALLOW_RESET=1 is required because the managed integration journey deletes all Xisnove rows")
		}
		harness := openStorageHarness(t, database.Config{
			Profile: database.ProfileTursoCloud, URL: rawURL, AuthToken: authToken,
		})
		resetIntegrationStorage(t, harness.primary)
		return harness
	}
	apiKey := os.Getenv("TURSO_API_KEY")
	if apiKey == "" {
		t.Skip("TURSO_API_KEY is not set")
	}
	group := os.Getenv("TURSO_GROUP")
	if group == "" {
		t.Fatal("TURSO_GROUP must name a dedicated deletion-enabled CI group")
	}
	disposable, err := tursotest.Provision(context.Background(), tursotest.Config{
		Token:         apiKey,
		Organization:  os.Getenv("TURSO_ORG"),
		Group:         group,
		DeleteTimeout: 2 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := disposable.Delete(ctx); err != nil {
			t.Errorf("delete managed Turso storage-matrix database: %v", err)
		}
	})
	return openStorageHarness(t, database.Config{
		Profile:   database.ProfileTursoCloud,
		URL:       disposable.URL,
		AuthToken: disposable.AuthToken,
	})
}

func resetIntegrationStorage(t *testing.T, handle *database.Handle) {
	t.Helper()
	for _, table := range []string{
		"idempotency_records",
		"api_tokens",
		"notification_delivery_attempts", "notification_outbox", "notification_routes",
		"notification_channels", "maintenance_intervals", "audit_events", "daily_uptime",
		"operation_leases", "incident_events", "incidents", "monitor_health", "location_health",
		"probe_results", "check_runs", "agent_enrollment_tokens", "agent_credentials", "agents", "monitor_locations",
		"monitors", "locations", "sessions", "admins",
	} {
		if _, err := handle.DB.ExecContext(context.Background(), "DELETE FROM "+table); err != nil {
			t.Fatalf("reset integration table %s: %v", table, err)
		}
	}
}

func openStorageHarness(t *testing.T, config database.Config) *storageHarness {
	t.Helper()
	ctx := context.Background()
	primary, err := database.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	harness := &storageHarness{primary: primary, config: config}
	t.Cleanup(func() { harness.close(t) })
	if err := primary.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := primary.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	secondary, err := database.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	harness.secondary = secondary
	if err := secondary.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	return harness
}

func (h *storageHarness) closeAndReopen(t *testing.T) *database.Handle {
	t.Helper()
	h.close(t)
	handle, err := database.Open(context.Background(), h.config)
	if err != nil {
		t.Fatal(err)
	}
	h.primary = handle
	if err := handle.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	return handle
}

func (h *storageHarness) close(t *testing.T) {
	t.Helper()
	for name, handle := range map[string]**database.Handle{
		"secondary": &h.secondary,
		"primary":   &h.primary,
	} {
		if *handle == nil {
			continue
		}
		if err := (*handle).Close(); err != nil {
			t.Errorf("close %s storage handle: %v", name, err)
		}
		*handle = nil
	}
}

func (h *storageHarness) String() string {
	return fmt.Sprintf("storage profile %s", h.config.Profile)
}
