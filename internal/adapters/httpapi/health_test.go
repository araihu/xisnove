package httpapi_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
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

func TestGetMonitorAvailabilityHistoryReturnsBoundedEnvelope(t *testing.T) {
	ctx := context.Background()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	monitorID := domain.MonitorID("00000000-0000-4000-8000-000000000011")
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID: monitorID, Name: "history fixture", Interval: time.Minute, Timeout: 5 * time.Second,
		FailureThreshold: 3, RecoveryThreshold: 2,
		HTTP:      domain.HTTPProbe{URL: "https://example.com", ExpectedStatus: []domain.StatusRange{{Min: 200, Max: 299}}},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlitestore.NewStore(db).Repositories().Monitors.Create(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	server := httpapi.NewServer(httpapi.ServerConfig{
		History: application.NewMonitorHistoryServiceWithClock(sqlitestore.NewStore(db), func() time.Time { return now }),
	})
	start, end := now.Add(-time.Hour), now
	limit := httpapi.HistoryLimit(8)
	response, err := server.GetMonitorAvailabilityHistory(ctx, httpapi.GetMonitorAvailabilityHistoryRequestObject{
		MonitorId: uuid.MustParse(string(monitorID)),
		Params:    httpapi.GetMonitorAvailabilityHistoryParams{StartsAt: (*httpapi.HistoryStartsAt)(&start), EndsAt: (*httpapi.HistoryEndsAt)(&end), Limit: &limit},
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, ok := response.(httpapi.GetMonitorAvailabilityHistory200JSONResponse)
	if !ok {
		t.Fatalf("response = %#v", response)
	}
	if envelope.MonitorId.String() != string(monitorID) || !envelope.StartsAt.Equal(start) || !envelope.EndsAt.Equal(end) || len(envelope.Samples) != 0 || envelope.Truncated {
		t.Fatalf("history envelope = %#v", envelope)
	}
}
