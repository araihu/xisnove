package integration_test

import (
	"testing"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/contracttest"
	"github.com/araihu/xisnove/internal/adapters/database"
)

func TestOperatorEdgeStorage(t *testing.T) {
	for _, test := range []struct {
		name    string
		profile database.Profile
	}{
		{name: "SQLite", profile: database.ProfileSQLite},
		{name: "TursoLocal", profile: database.ProfileTursoLocal},
		{name: "Postgres", profile: database.ProfilePostgres},
		{name: "TursoCloud", profile: database.ProfileTursoCloud},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			contracttest.RunDiscovery(t, func(t *testing.T) port.UnitOfWork {
				var harness *storageHarness
				switch test.profile {
				case database.ProfileSQLite, database.ProfileTursoLocal:
					harness = newFileStorageHarness(t, test.profile)
				case database.ProfilePostgres:
					harness = newPostgresStorageHarness(t)
				case database.ProfileTursoCloud:
					harness = newTursoCloudStorageHarness(t)
				}
				return harness.primary.Store
			})
		})
	}
}
