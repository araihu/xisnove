package postgres_test

import (
	"context"
	"net/url"
	"testing"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/contracttest"
	"github.com/araihu/xisnove/internal/adapters/postgres"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestPublicStatusPersistenceConformance(t *testing.T) {
	ctx := context.Background()
	container, err := pgcontainer.Run(ctx, "postgres:17-alpine",
		pgcontainer.WithDatabase("xisnove"), pgcontainer.WithUsername("xisnove"), pgcontainer.WithPassword("xisnove"),
		pgcontainer.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	rawURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	contracttest.RunPublicStatus(t, func(t *testing.T) (port.UnitOfWork, port.PublicStatusUnitOfWork) {
		db, err := postgres.Open(ctx, parsed.String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := postgres.Migrate(ctx, db); err != nil {
			t.Fatal(err)
		}
		return postgres.NewStore(db), postgres.NewPublicStatusUnitOfWork(db)
	})
}
