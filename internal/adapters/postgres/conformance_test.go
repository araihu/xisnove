package postgres_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	application "github.com/araihu/xisnove/application/port"
	conformance "github.com/araihu/xisnove/contracttest"
	"github.com/araihu/xisnove/internal/adapters/postgres"
	postgrescontainer "github.com/araihu/xisnove/internal/testsupport/postgrescontainer"
	"github.com/google/uuid"
)

func TestPersistenceConformance(t *testing.T) {
	baseURL := postgrescontainer.URL(t, os.Getenv("XISNOVE_TEST_POSTGRES_URL"))
	factory := func(t *testing.T) application.UnitOfWork {
		t.Helper()
		ctx := context.Background()
		admin, err := postgres.Open(ctx, baseURL)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = admin.Close() })

		schema := "xisnove_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := admin.ExecContext(
				context.Background(),
				"DROP SCHEMA "+schema+" CASCADE",
			); err != nil {
				t.Errorf("drop PostgreSQL test schema: %v", err)
			}
		})

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
			t.Fatal(fmt.Errorf("migrate PostgreSQL test schema: %w", err))
		}
		return postgres.NewStore(db)
	}
	conformance.Run(t, factory)
	conformance.RunIdempotency(t, factory)
	conformance.RunManagement(t, factory)
}
