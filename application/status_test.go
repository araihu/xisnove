package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

const (
	publicStatusMonitor1 = "00000000-0000-4000-8000-000000000101"
	publicStatusMonitor2 = "00000000-0000-4000-8000-000000000102"
	publicStatusIncident = "00000000-0000-4000-8000-000000000103"
)

var publicStatusNow = time.Date(2026, 7, 26, 15, 30, 0, 0, time.UTC)

func TestPublicStatusUsesOneReadTransaction(t *testing.T) {
	store := &publicStatusStore{repository: &publicStatusRepository{}}
	service := newPublicStatusService(t, store, 30)

	if _, err := service.Get(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.views != 1 {
		t.Fatalf("View calls = %d, want 1", store.views)
	}
}

func TestPublicStatusAggregatePrecedence(t *testing.T) {
	states := []domain.HealthState{
		domain.HealthUp,
		domain.HealthPending,
		domain.HealthUnknown,
		domain.HealthDegraded,
		domain.HealthDown,
	}
	for wantIndex, want := range []domain.HealthState{
		domain.HealthUp,
		domain.HealthPending,
		domain.HealthUnknown,
		domain.HealthDegraded,
		domain.HealthDown,
	} {
		t.Run(string(want), func(t *testing.T) {
			rows := make([]port.PublicMonitorProjection, 0, wantIndex+1)
			for index, state := range states[:wantIndex+1] {
				rows = append(rows, port.PublicMonitorProjection{
					ID:   domain.MonitorID(publicStatusMonitor1[:35] + string(rune('1'+index))),
					Name: "monitor", State: state,
				})
			}
			service := newPublicStatusService(t, &publicStatusStore{
				repository: &publicStatusRepository{monitors: rows},
			}, 30)
			page, err := service.Get(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if page.State != want {
				t.Fatalf("state = %q, want %q", page.State, want)
			}
		})
	}
}

func TestPublicStatusIncludesActiveIncidentAndBoundedUptime(t *testing.T) {
	incident := domain.Incident{
		ID: publicStatusIncident, MonitorID: publicStatusMonitor1,
		State: domain.HealthDown, Severity: domain.IncidentCritical,
		OpenedAt: publicStatusNow.Add(-time.Hour), LastTransitionAt: publicStatusNow.Add(-time.Hour),
	}
	repository := &publicStatusRepository{
		monitors: []port.PublicMonitorProjection{{
			ID: publicStatusMonitor1, Name: "edge", Description: "public edge",
			State: domain.HealthDown, LastTransitionAt: publicStatusNow.Add(-time.Hour),
			ActiveIncident: &incident,
		}},
		uptime: map[domain.MonitorID][]port.DailyUptimeRecord{
			publicStatusMonitor1: {{
				MonitorID: publicStatusMonitor1,
				Day:       time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC),
				Passing:   9, Failing: 1, Observed: 24 * time.Hour,
			}},
		},
	}
	service := newPublicStatusService(t, &publicStatusStore{repository: repository}, 30)
	page, err := service.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !repository.start.Equal(time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC)) ||
		!repository.end.Equal(time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("uptime range = [%s,%s)", repository.start, repository.end)
	}
	if len(page.Monitors) != 1 || page.Monitors[0].ActiveIncident == nil ||
		page.Monitors[0].ActiveIncident.ID != publicStatusIncident ||
		len(page.Monitors[0].Uptime) != 1 {
		t.Fatalf("page = %#v", page)
	}
	if len(page.ActiveIncidents) != 1 || page.ActiveIncidents[0].MonitorName != "edge" {
		t.Fatalf("active incidents = %#v", page.ActiveIncidents)
	}

	repository.monitors[0].Name = "changed"
	repository.uptime[publicStatusMonitor1][0].Passing = 0
	if page.Monitors[0].Name != "edge" || page.Monitors[0].Uptime[0].Passing != 9 {
		t.Fatalf("response aliases repository state: %#v", page.Monitors[0])
	}
}

func TestPublicStatusEmptyPageIsUp(t *testing.T) {
	service := newPublicStatusService(t, &publicStatusStore{repository: &publicStatusRepository{}}, 30)
	page, err := service.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if page.State != domain.HealthUp || page.Monitors == nil || page.ActiveIncidents == nil ||
		!page.GeneratedAt.Equal(publicStatusNow) {
		t.Fatalf("page = %#v", page)
	}
}

func TestPublicStatusUnknownAndPendingAreStable(t *testing.T) {
	service := newPublicStatusService(t, &publicStatusStore{repository: &publicStatusRepository{
		monitors: []port.PublicMonitorProjection{
			{ID: publicStatusMonitor1, Name: "unknown", State: domain.HealthUnknown},
			{ID: publicStatusMonitor2, Name: "pending", State: domain.HealthPending},
		},
	}}, 30)
	page, err := service.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if page.Monitors[0].State != domain.HealthUnknown ||
		page.Monitors[1].State != domain.HealthPending || page.State != domain.HealthUnknown {
		t.Fatalf("page = %#v", page)
	}
}

func TestPublicStatusValidatesHistoryDaysAndWrapsStorageErrors(t *testing.T) {
	if _, err := application.NewPublicStatusService(application.PublicStatusServiceConfig{
		Store: &publicStatusStore{}, HistoryDays: 91,
	}); err == nil {
		t.Fatal("HistoryDays 91 was accepted")
	}
	if _, err := application.NewPublicStatusService(application.PublicStatusServiceConfig{
		Store: &publicStatusStore{}, HistoryDays: 90,
	}); err != nil {
		t.Fatalf("HistoryDays 90 was rejected: %v", err)
	}
	stop := errors.New("storage unavailable: secret detail")
	service := newPublicStatusService(t, &publicStatusStore{err: stop}, 30)
	if _, err := service.Get(context.Background()); !errors.Is(err, stop) {
		t.Fatalf("error = %v", err)
	}
}

func TestPublicStatusDefaultsToThirtyDays(t *testing.T) {
	repository := &publicStatusRepository{monitors: []port.PublicMonitorProjection{{
		ID: publicStatusMonitor1, Name: "edge", State: domain.HealthUp,
	}}}
	service, err := application.NewPublicStatusService(application.PublicStatusServiceConfig{
		Store: &publicStatusStore{repository: repository},
		Now:   func() time.Time { return publicStatusNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC); !repository.start.Equal(want) {
		t.Fatalf("default uptime start = %s, want %s", repository.start, want)
	}
}

func newPublicStatusService(t *testing.T, store port.PublicStatusUnitOfWork, days int) *application.PublicStatusService {
	t.Helper()
	service, err := application.NewPublicStatusService(application.PublicStatusServiceConfig{
		Store: store, HistoryDays: days, Now: func() time.Time { return publicStatusNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type publicStatusStore struct {
	repository *publicStatusRepository
	views      int
	err        error
}

func (s *publicStatusStore) View(ctx context.Context, fn func(context.Context, port.PublicStatusRepositories) error) error {
	s.views++
	if s.err != nil {
		return s.err
	}
	return fn(ctx, port.PublicStatusRepositories{Status: s.repository, Retention: s.repository})
}

type publicStatusRepository struct {
	monitors []port.PublicMonitorProjection
	uptime   map[domain.MonitorID][]port.DailyUptimeRecord
	start    time.Time
	end      time.Time
}

func (r *publicStatusRepository) ListMonitors(context.Context) ([]port.PublicMonitorProjection, error) {
	return r.monitors, nil
}

func (r *publicStatusRepository) ListDailyUptime(_ context.Context, monitorID domain.MonitorID, start, end time.Time) ([]port.DailyUptimeRecord, error) {
	r.start, r.end = start, end
	return r.uptime[monitorID], nil
}

func (*publicStatusRepository) ClaimLease(context.Context, port.OperationLeaseRecord, time.Time) (port.OperationLeaseRecord, error) {
	panic("unexpected ClaimLease")
}
func (*publicStatusRepository) UpdateLease(context.Context, port.OperationLeaseRecord) (bool, error) {
	panic("unexpected UpdateLease")
}
func (*publicStatusRepository) ReleaseLease(context.Context, string, []byte) (bool, error) {
	panic("unexpected ReleaseLease")
}
func (*publicStatusRepository) ListAggregationResults(context.Context, time.Time, time.Time, time.Time, string, int) ([]port.AggregationResultRecord, error) {
	panic("unexpected ListAggregationResults")
}
func (*publicStatusRepository) UpsertDailyUptime(context.Context, port.DailyUptimeRecord) error {
	panic("unexpected UpsertDailyUptime")
}
func (*publicStatusRepository) DeleteExpiredResults(context.Context, time.Time, int) (int64, error) {
	panic("unexpected DeleteExpiredResults")
}
func (*publicStatusRepository) DeleteExpiredDailyUptime(context.Context, time.Time, int) (int64, error) {
	panic("unexpected DeleteExpiredDailyUptime")
}
