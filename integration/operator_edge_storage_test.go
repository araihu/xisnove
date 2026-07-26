package integration_test

import (
	"testing"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/contracttest"
	"github.com/araihu/xisnove/internal/adapters/database"
)

func TestOperatorEdgeStorage(t *testing.T) {
	for _, profile := range []database.Profile{
		database.ProfileSQLite,
		database.ProfileTursoLocal,
		database.ProfilePostgres,
		database.ProfileTursoCloud,
	} {
		profile := profile
		t.Run(string(profile), func(t *testing.T) {
			var harness *storageHarness
			switch profile {
			case database.ProfileSQLite, database.ProfileTursoLocal:
				harness = newFileStorageHarness(t, profile)
			case database.ProfilePostgres:
				harness = newPostgresStorageHarness(t)
			case database.ProfileTursoCloud:
				harness = newTursoCloudStorageHarness(t)
			}
			contracttest.RunOperatorEdge(t, func(*testing.T) port.UnitOfWork { return harness.primary.Store })
		})
	}
}
