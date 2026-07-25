package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestWithinTxRollsBackRepositoryWrites(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	stop := errors.New("stop transaction")

	err := store.WithinTx(ctx, func(repositories application.Repositories) error {
		location, createErr := domain.NewLocation("location-1", "home", time.Now())
		if createErr != nil {
			return createErr
		}
		if createErr = repositories.Locations.Create(ctx, location); createErr != nil {
			return createErr
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("error = %v", err)
	}

	_, err = store.Repositories().Locations.Get(ctx, "location-1")
	if !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("Get error = %v", err)
	}
}

func TestWithinTxCommitsRepositoryWrites(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	createdAt := time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC)

	err := store.WithinTx(ctx, func(repositories application.Repositories) error {
		location, createErr := domain.NewLocation("location-1", "home", createdAt)
		if createErr != nil {
			return createErr
		}
		return repositories.Locations.Create(ctx, location)
	})
	if err != nil {
		t.Fatal(err)
	}

	location, err := store.Repositories().Locations.Get(ctx, "location-1")
	if err != nil {
		t.Fatal(err)
	}
	if location.Name != "home" || !location.CreatedAt.Equal(createdAt) {
		t.Fatalf("Location = %#v", location)
	}
}

func TestMonitorRepositoryRoundTripsHTTPConfiguration(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	repositories := store.Repositories()
	now := time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC)

	location, err := domain.NewLocation("location-1", "home", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Locations.Create(ctx, location); err != nil {
		t.Fatal(err)
	}
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID:                "monitor-1",
		Name:              "router",
		Interval:          time.Minute,
		Timeout:           5 * time.Second,
		FailureThreshold:  3,
		RecoveryThreshold: 2,
		HTTP: domain.HTTPProbe{
			Method:          "GET",
			URL:             "https://router.example.test/health",
			ExpectedStatus:  []domain.StatusRange{{Min: 200, Max: 299}},
			BodyContains:    []string{"ok"},
			FollowRedirects: false,
		},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Monitors.Create(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Monitors.AssignLocation(ctx, application.MonitorLocation{
		MonitorID: "monitor-1", LocationID: "location-1", Required: true,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := repositories.Monitors.Get(ctx, "monitor-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != monitor.Name ||
		got.HTTP.URL != monitor.HTTP.URL ||
		len(got.HTTP.ExpectedStatus) != 1 ||
		got.HTTP.ExpectedStatus[0] != (domain.StatusRange{Min: 200, Max: 299}) {
		t.Fatalf("Monitor = %#v", got)
	}
}

func TestHealthRepositoryRoundTripsRequiredProjection(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	repositories := store.Repositories()
	now := time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC)
	seedMonitorAndLocation(t, ctx, repositories, now)

	locationHealth := domain.LocationHealth{
		MonitorID:           "monitor-1",
		LocationID:          "location-1",
		State:               domain.HealthDown,
		ConsecutiveFailures: 3,
		LastObservedAt:      now,
		LastTransitionAt:    now,
	}
	if err := repositories.Health.UpsertLocation(ctx, locationHealth); err != nil {
		t.Fatal(err)
	}
	monitorHealth := domain.MonitorHealth{
		MonitorID: "monitor-1", State: domain.HealthDown, LastTransitionAt: now,
	}
	if err := repositories.Health.UpsertMonitor(ctx, monitorHealth); err != nil {
		t.Fatal(err)
	}

	locations, err := repositories.Health.ListRequiredLocations(ctx, "monitor-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(locations) != 1 || locations[0].State != domain.HealthDown {
		t.Fatalf("locations = %#v", locations)
	}
	got, err := repositories.Health.GetMonitor(ctx, "monitor-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.HealthDown || !got.LastTransitionAt.Equal(now) {
		t.Fatalf("MonitorHealth = %#v", got)
	}
}

func newStore(t *testing.T) application.Store {
	t.Helper()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return sqlitestore.NewStore(db)
}

func seedMonitorAndLocation(
	t *testing.T,
	ctx context.Context,
	repositories application.Repositories,
	now time.Time,
) {
	t.Helper()
	location, err := domain.NewLocation("location-1", "home", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Locations.Create(ctx, location); err != nil {
		t.Fatal(err)
	}
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID:                "monitor-1",
		Name:              "router",
		Interval:          time.Minute,
		Timeout:           5 * time.Second,
		FailureThreshold:  3,
		RecoveryThreshold: 2,
		HTTP: domain.HTTPProbe{
			URL:            "https://router.example.test/health",
			ExpectedStatus: []domain.StatusRange{{Min: 200, Max: 299}},
		},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Monitors.Create(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Monitors.AssignLocation(ctx, application.MonitorLocation{
		MonitorID: "monitor-1", LocationID: "location-1", Required: true,
	}); err != nil {
		t.Fatal(err)
	}
}
