package tursolocal_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/xisnove/domain"
	xisdatabase "github.com/araihu/xisnove/internal/adapters/database"
)

func TestOpenMigrateAndReopenLocalTurso(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	config := xisdatabase.Config{
		Profile: xisdatabase.ProfileTursoLocal,
		URL:     filepath.Join(t.TempDir(), "xisnove.turso"),
	}
	handle, err := xisdatabase.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Migrate(ctx); err != nil {
		_ = handle.Close()
		t.Fatal(err)
	}
	if err := handle.Ready(ctx); err != nil {
		_ = handle.Close()
		t.Fatal(err)
	}

	createdAt := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	location, err := domain.NewLocation("location-1", "homelab", createdAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Store.Repositories().Locations.Create(ctx, location); err != nil {
		_ = handle.Close()
		t.Fatal(err)
	}
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID:                "monitor-1",
		Name:              "homelab gateway",
		Interval:          time.Minute,
		Timeout:           5 * time.Second,
		FailureThreshold:  3,
		RecoveryThreshold: 2,
		HTTP: domain.HTTPProbe{
			Method:         "GET",
			URL:            "https://gateway.example.test/health",
			ExpectedStatus: []domain.StatusRange{{Min: 200, Max: 299}},
		},
		CreatedAt: createdAt,
	})
	if err != nil {
		_ = handle.Close()
		t.Fatal(err)
	}
	if err := handle.Store.Repositories().Monitors.Create(ctx, monitor); err != nil {
		_ = handle.Close()
		t.Fatal(err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := xisdatabase.Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Store.Repositories().Locations.Get(ctx, location.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != location {
		t.Fatalf("Location = %#v, want %#v", got, location)
	}
	gotMonitor, err := reopened.Store.Repositories().Monitors.Get(ctx, monitor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotMonitor.Name != monitor.Name || gotMonitor.HTTP.URL != monitor.HTTP.URL {
		t.Fatalf("Monitor = %#v, want %#v", gotMonitor, monitor)
	}
}
