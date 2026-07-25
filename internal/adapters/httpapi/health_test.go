package httpapi_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/internal/adapters/httpapi"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestGetActiveMonitorIncidentReturnsNoContentWhenAbsent(t *testing.T) {
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	server := httpapi.NewServer(httpapi.ServerConfig{
		Health: application.NewHealthService(sqlitestore.NewStore(db)),
	})
	response, err := server.GetActiveMonitorIncident(
		context.Background(),
		httpapi.GetActiveMonitorIncidentRequestObject{
			MonitorId: uuid.MustParse("00000000-0000-4000-8000-000000000001"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := response.(httpapi.GetActiveMonitorIncident204Response); !ok {
		t.Fatalf("response = %#v", response)
	}
}
