package postgres_test

import (
	"context"
	"os"
	"testing"

	"github.com/araihu/xisnove/internal/adapters/postgres"
)

func TestMigrateAndReady(t *testing.T) {
	url := os.Getenv("XISNOVE_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("XISNOVE_TEST_POSTGRES_URL is not set")
	}
	ctx := context.Background()
	db, err := postgres.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := postgres.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := postgres.Ready(ctx, db); err != nil {
		t.Fatal(err)
	}
}
