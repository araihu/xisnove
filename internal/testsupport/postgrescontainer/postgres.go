// Package postgrescontainer provisions disposable PostgreSQL instances for
// integration and adapter tests when no external test database is configured.
package postgrescontainer

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const image = "postgres:18-alpine"

// URL returns the configured external PostgreSQL URL or starts a disposable
// PostgreSQL 18 container and registers its termination with t.Cleanup. Tests
// are skipped when no external URL is provided and no container runtime exists.
func URL(t *testing.T, external string) string {
	t.Helper()
	if external != "" {
		return external
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()
	container, err := tcpostgres.Run(
		ctx,
		image,
		tcpostgres.WithDatabase("xisnove"),
		tcpostgres.WithUsername("xisnove"),
		tcpostgres.WithPassword("xisnove-test-password"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start disposable PostgreSQL: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Errorf("terminate disposable PostgreSQL: %v", err)
		}
	})
	connection, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("build disposable PostgreSQL connection string: %v", err)
	}
	return connection
}
