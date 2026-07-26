package tursocloud_test

import (
	"context"
	"testing"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/contracttest"
	"github.com/araihu/xisnove/internal/adapters/tursocloud"
)

func TestPublicStatusPersistenceConformance(t *testing.T) {
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
	resetManagedTurso(t, db)
	contracttest.RunPublicStatus(t, func(*testing.T) (port.UnitOfWork, port.PublicStatusUnitOfWork) {
		return tursocloud.NewStore(db), tursocloud.NewPublicStatusUnitOfWork(db)
	})
}
