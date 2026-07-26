package contracttest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

type PublicStatusFactory func(*testing.T) (port.UnitOfWork, port.PublicStatusUnitOfWork)

// RunPublicStatus verifies the privacy, ordering, missing-health, active
// incident, and bounded-uptime behavior shared by every relational profile.
func RunPublicStatus(t *testing.T, factory PublicStatusFactory) {
	t.Helper()
	t.Run("public status projection", func(t *testing.T) {
		ctx := context.Background()
		writeStore, readStore := factory(t)
		now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
		const (
			privateID  = domain.MonitorID("00000000-0000-4000-8000-000000000201")
			pendingID  = domain.MonitorID("00000000-0000-4000-8000-000000000202")
			unknownID  = domain.MonitorID("00000000-0000-4000-8000-000000000203")
			incidentID = domain.IncidentID("00000000-0000-4000-8000-000000000204")
			bulkCount  = 999
		)
		makeMonitor := func(id domain.MonitorID, name, description string, order int32, public bool) domain.Monitor {
			t.Helper()
			monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
				ID: id, Name: name, Description: description, DisplayOrder: order, Public: public,
				Interval: time.Minute, Timeout: 5 * time.Second,
				FailureThreshold: 1, RecoveryThreshold: 1,
				HTTP: domain.HTTPProbe{URL: "https://example.test/health"}, CreatedAt: now,
			})
			if err != nil {
				t.Fatal(err)
			}
			return monitor
		}
		err := writeStore.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
			for _, monitor := range []domain.Monitor{
				makeMonitor(privateID, "private", "secret description", 0, false),
				makeMonitor(unknownID, "unknown", "public unknown", 10, true),
				makeMonitor(pendingID, "pending", "public pending", 10, true),
			} {
				if err := repositories.Monitors.Create(ctx, monitor); err != nil {
					return err
				}
			}
			for index := 0; index < bulkCount; index++ {
				id := domain.MonitorID(fmt.Sprintf("10000000-0000-4000-8000-%012d", index))
				if err := repositories.Monitors.Create(ctx, makeMonitor(
					id, fmt.Sprintf("bulk-%04d", index), "public bulk monitor", int32(100+index), true,
				)); err != nil {
					return err
				}
			}
			if err := repositories.Health.UpsertMonitor(ctx, domain.MonitorHealth{
				MonitorID: unknownID, State: domain.HealthUnknown, LastTransitionAt: now.Add(-time.Hour),
			}); err != nil {
				return err
			}
			if err := repositories.Incidents.Open(ctx, domain.Incident{
				ID: incidentID, MonitorID: unknownID, State: domain.HealthUnknown,
				Severity: domain.IncidentWarning, OpenedAt: now.Add(-2 * time.Hour),
				LastTransitionAt: now.Add(-time.Hour),
			}); err != nil {
				return err
			}
			for _, day := range []time.Time{now.AddDate(0, 0, -31), now.AddDate(0, 0, -1), now} {
				if err := repositories.Retention.UpsertDailyUptime(ctx, port.DailyUptimeRecord{
					MonitorID: unknownID, Day: day, Passing: 9, Failing: 1,
					Observed: 24 * time.Hour, UpdatedAt: now,
				}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}

		var monitors []port.PublicMonitorProjection
		var uptime []port.DailyUptimeRecord
		err = readStore.View(ctx, func(ctx context.Context, repositories port.PublicStatusRepositories) error {
			var err error
			monitors, err = repositories.Status.ListMonitors(ctx)
			if err != nil {
				return err
			}
			uptime, err = repositories.Retention.ListDailyUptime(
				ctx, unknownID,
				time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC),
				time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC),
			)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(monitors) != 1001 || monitors[0].ID != pendingID || monitors[1].ID != unknownID {
			t.Fatalf("ordered public monitors = %#v", monitors)
		}
		if want := domain.MonitorID("10000000-0000-4000-8000-000000000998"); monitors[len(monitors)-1].ID != want {
			t.Fatalf("last public monitor = %q, want %q", monitors[len(monitors)-1].ID, want)
		}
		if monitors[0].State != domain.HealthPending || !monitors[0].LastTransitionAt.IsZero() {
			t.Fatalf("missing health projection = %#v", monitors[0])
		}
		if monitors[1].State != domain.HealthUnknown || monitors[1].ActiveIncident == nil ||
			monitors[1].ActiveIncident.ID != incidentID {
			t.Fatalf("unknown incident projection = %#v", monitors[1])
		}
		if len(uptime) != 1 || uptime[0].Day.Format(time.DateOnly) != "2026-07-25" {
			t.Fatalf("bounded uptime = %#v", uptime)
		}

		stop := errors.New("stop status view")
		err = readStore.View(ctx, func(ctx context.Context, repositories port.PublicStatusRepositories) error {
			if _, err := repositories.Status.ListMonitors(ctx); err != nil {
				return err
			}
			return stop
		})
		if !errors.Is(err, stop) {
			t.Fatalf("aborted view error = %v", err)
		}
		err = readStore.View(ctx, func(ctx context.Context, repositories port.PublicStatusRepositories) error {
			rows, err := repositories.Status.ListMonitors(ctx)
			if err != nil {
				return err
			}
			if len(rows) != 1001 {
				t.Fatalf("rows after aborted view = %#v", rows)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}
