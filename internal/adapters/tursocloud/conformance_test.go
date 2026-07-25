package tursocloud_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/araihu/xisnove/application"
	conformance "github.com/araihu/xisnove/contracttest"
	"github.com/araihu/xisnove/internal/adapters/tursocloud"
	tursotest "github.com/araihu/xisnove/internal/testsupport/tursocloud"
)

func TestPersistenceConformance(t *testing.T) {
	ctx := context.Background()
	rawURL, token := managedTestDatabase(t, ctx)
	db, err := tursocloud.Open(ctx, rawURL, token)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := tursocloud.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := tursocloud.Ready(ctx, db); err != nil {
		t.Fatal(err)
	}

	conformance.Run(t, func(t *testing.T) application.Store {
		t.Helper()
		resetManagedTurso(t, db)
		return tursocloud.NewStore(db)
	})
}

func managedTestDatabase(t *testing.T, ctx context.Context) (string, string) {
	t.Helper()
	rawURL := os.Getenv("XISNOVE_TEST_TURSO_URL")
	token := os.Getenv("XISNOVE_TEST_TURSO_TOKEN")
	if rawURL != "" || token != "" {
		if rawURL == "" || token == "" {
			t.Fatal("XISNOVE_TEST_TURSO_URL and XISNOVE_TEST_TURSO_TOKEN must be set together")
		}
		return rawURL, token
	}

	platformToken := os.Getenv("TURSO_API_KEY")
	if platformToken == "" {
		t.Skip("managed Turso credentials are not set")
	}
	database, err := tursotest.Provision(ctx, tursotest.Config{
		Token:        platformToken,
		Organization: os.Getenv("TURSO_ORG"),
		Group:        os.Getenv("TURSO_GROUP"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Delete(context.Background()); err != nil {
			t.Errorf("delete managed Turso conformance database: %v", err)
		}
	})
	return database.URL, database.AuthToken
}

func resetManagedTurso(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, table := range []string{
		"incident_events",
		"incidents",
		"monitor_health",
		"location_health",
		"probe_results",
		"check_runs",
		"agent_enrollment_tokens",
		"agents",
		"monitor_locations",
		"monitors",
		"locations",
		"sessions",
		"admins",
	} {
		if _, err := database.ExecContext(
			context.Background(),
			"DELETE FROM "+table,
		); err != nil {
			t.Fatalf("reset managed Turso table %s: %v", table, err)
		}
	}
}
