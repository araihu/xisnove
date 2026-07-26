package postgres_test

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/contracttest"
	"github.com/araihu/xisnove/internal/adapters/postgres"
	postgrescontainer "github.com/araihu/xisnove/internal/testsupport/postgrescontainer"
	"github.com/google/uuid"
)

func TestDiscoveryPersistenceConformance(t *testing.T) {
	baseURL := postgrescontainer.URL(t, os.Getenv("XISNOVE_TEST_POSTGRES_URL"))
	contracttest.RunDiscovery(t, func(t *testing.T) port.UnitOfWork {
		ctx := context.Background()
		admin, err := postgres.Open(ctx, baseURL)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = admin.Close() })
		schema := "xisnove_discovery_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
		databaseURL, err := url.Parse(baseURL)
		if err != nil {
			t.Fatal(err)
		}
		query := databaseURL.Query()
		query.Set("search_path", schema)
		databaseURL.RawQuery = query.Encode()
		db, err := postgres.Open(ctx, databaseURL.String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := postgres.Migrate(ctx, db); err != nil {
			t.Fatal(err)
		}
		return postgres.NewStore(db)
	})
}
