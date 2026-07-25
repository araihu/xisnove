package contracttest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	application "github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

// Factory provisions an isolated UnitOfWork for one contract subtest.
// Adapter setup and cleanup belong in the factory, not in this package.
type Factory func(*testing.T) application.UnitOfWork

const (
	locationID = domain.LocationID("00000000-0000-4000-8000-000000000001")
	monitorID  = domain.MonitorID("00000000-0000-4000-8000-000000000002")
	runID      = domain.CheckRunID("00000000-0000-4000-8000-000000000003")
)

// Run executes the persistence contract against each UnitOfWork returned by
// factory. The suite covers transactions, idempotency, leases, notifications,
// audit records, and retention behavior.
func Run(t *testing.T, factory Factory) {
	t.Helper()

	t.Run("transaction rollback", func(t *testing.T) {
		testTransactionRollback(t, factory(t))
	})
	t.Run("duplicate result", func(t *testing.T) {
		testDuplicateResult(t, factory(t))
	})
	t.Run("one active incident", func(t *testing.T) {
		testOneActiveIncident(t, factory(t))
	})
	t.Run("competing and expired lease", func(t *testing.T) {
		testCompetingAndExpiredLease(t, factory(t))
	})
	t.Run("stale compare and set", func(t *testing.T) {
		testStaleCompareAndSet(t, factory(t))
	})
	t.Run("schedule idempotency", func(t *testing.T) {
		testScheduleIdempotency(t, factory(t))
	})
	t.Run("notification persistence", func(t *testing.T) {
		testNotificationPersistence(t, factory(t))
	})
	t.Run("notification ordering and competing claims", func(t *testing.T) {
		testNotificationOrderingAndCompetingClaims(t, factory(t))
	})
	t.Run("maintenance and operation leases", func(t *testing.T) {
		testMaintenanceAndOperationLeases(t, factory(t))
	})
	t.Run("audit and bounded daily retention", func(t *testing.T) {
		testAuditAndBoundedDailyRetention(t, factory(t))
	})
	t.Run("aggregation cursor and bounded raw retention", func(t *testing.T) {
		testAggregationCursorAndBoundedRawRetention(t, factory(t))
	})
}

func transact(
	t *testing.T,
	ctx context.Context,
	unitOfWork application.UnitOfWork,
	operation func(context.Context, application.Repositories) error,
) {
	t.Helper()
	if err := unitOfWork.Transact(ctx, operation); err != nil {
		t.Fatalf("Transact() error = %v", err)
	}
}

func view(
	t *testing.T,
	ctx context.Context,
	unitOfWork application.UnitOfWork,
	operation func(context.Context, application.Repositories) error,
) {
	t.Helper()
	if err := unitOfWork.View(ctx, operation); err != nil {
		t.Fatalf("View() error = %v", err)
	}
}

func testTransactionRollback(t *testing.T, store application.UnitOfWork) {
	t.Helper()
	ctx := context.Background()
	stop := errors.New("stop")

	err := store.Transact(ctx, func(ctx context.Context, repositories application.Repositories) error {
		location := mustLocation(t, locationID)
		if err := repositories.Locations.Create(ctx, location); err != nil {
			return err
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("Transact() error = %v, want %v", err, stop)
	}
	view(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		_, err = repositories.Locations.Get(ctx, locationID)
		if !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("Get() error = %v, want ErrNotFound", err)
		}
		return nil
	})
}

func testDuplicateResult(t *testing.T, store application.UnitOfWork) {
	t.Helper()
	ctx := context.Background()
	transact(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		fixture := seed(t, ctx, repositories, 1)
		result := application.ProbeResultRecord{
			ID:         "00000000-0000-4000-8000-000000000004",
			RunID:      fixture.runID,
			AgentID:    fixture.agentIDs[0],
			StartedAt:  fixture.now,
			FinishedAt: fixture.now.Add(10 * time.Millisecond),
			ReceivedAt: fixture.now.Add(time.Second),
			Passed:     true,
			Latency:    10 * time.Millisecond,
		}
		inserted, err := repositories.Results.Insert(ctx, result)
		if err != nil || !inserted {
			t.Fatalf("first Insert() = %v, %v", inserted, err)
		}
		inserted, err = repositories.Results.Insert(ctx, result)
		if err != nil || inserted {
			t.Fatalf("duplicate ID Insert() = %v, %v", inserted, err)
		}
		result.ID = "00000000-0000-4000-8000-000000000005"
		inserted, err = repositories.Results.Insert(ctx, result)
		if err != nil || inserted {
			t.Fatalf("duplicate run Insert() = %v, %v", inserted, err)
		}

		return nil
	})
}

func testOneActiveIncident(t *testing.T, store application.UnitOfWork) {
	t.Helper()
	ctx := context.Background()
	var second domain.Incident
	transact(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		fixture := seed(t, ctx, repositories, 1)
		first := domain.Incident{
			ID:               "00000000-0000-4000-8000-000000000006",
			MonitorID:        fixture.monitor.ID,
			State:            domain.HealthDown,
			Severity:         domain.IncidentCritical,
			OpenedAt:         fixture.now,
			LastTransitionAt: fixture.now,
		}
		if err := repositories.Incidents.Open(ctx, first); err != nil {
			return err
		}
		second = first
		second.ID = "00000000-0000-4000-8000-000000000007"
		return nil
	})
	err := store.Transact(ctx, func(
		ctx context.Context,
		repositories application.Repositories,
	) error {
		return repositories.Incidents.Open(ctx, second)
	})
	if !errors.Is(err, application.ErrConflict) {
		t.Fatalf("second Open() error = %v, want ErrConflict", err)
	}
}

func testCompetingAndExpiredLease(t *testing.T, store application.UnitOfWork) {
	t.Helper()
	ctx := context.Background()
	var fixture seeded
	transact(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		fixture = seed(t, ctx, repositories, 2)
		return nil
	})

	start := make(chan struct{})
	records := make([]application.RunRecord, 2)
	errs := make([]error, 2)
	var group sync.WaitGroup
	for index, agentID := range fixture.agentIDs {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			err := store.Transact(ctx, func(
				ctx context.Context,
				repositories application.Repositories,
			) error {
				records[index], errs[index] = repositories.Runs.ClaimProbe(
					ctx,
					application.ClaimRunParams{
						AgentID:        agentID,
						Capabilities:   []domain.AgentCapability{domain.CapabilityHTTP},
						LeaseTokenHash: []byte(fmt.Sprintf("lease-%d", index)),
						LeaseExpiresAt: fixture.now.Add(30 * time.Second),
						Now:            fixture.now,
					},
				)
				return nil
			})
			if err != nil {
				errs[index] = err
			}
		}()
	}
	close(start)
	group.Wait()

	winner := -1
	for index, err := range errs {
		if err == nil {
			if winner >= 0 {
				t.Fatalf("multiple claim winners: %#v", records)
			}
			winner = index
			continue
		}
		if !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("ClaimProbe() error = %v, want ErrNotFound", err)
		}
	}
	if winner < 0 {
		t.Fatalf("no claim winner: %v", errs)
	}

	loser := 1 - winner
	var reclaimed application.RunRecord
	transact(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		var err error
		reclaimed, err = repositories.Runs.ClaimProbe(
			ctx,
			application.ClaimRunParams{
				AgentID:        fixture.agentIDs[loser],
				Capabilities:   []domain.AgentCapability{domain.CapabilityHTTP},
				LeaseTokenHash: []byte("replacement-lease"),
				LeaseExpiresAt: fixture.now.Add(2 * time.Minute),
				Now:            fixture.now.Add(time.Minute),
			},
		)
		return err
	})
	if reclaimed.ID != fixture.runID || reclaimed.LeaseAttempt != 2 {
		t.Fatalf("reclaimed run = %#v", reclaimed)
	}
}

func testStaleCompareAndSet(t *testing.T, store application.UnitOfWork) {
	t.Helper()
	ctx := context.Background()
	transact(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		fixture := seed(t, ctx, repositories, 1)
		staleAt := fixture.now.Add(time.Minute)
		health := domain.LocationHealth{
			MonitorID:        fixture.monitor.ID,
			LocationID:       fixture.location.ID,
			State:            domain.HealthUp,
			LastObservedAt:   fixture.now,
			LastTransitionAt: fixture.now,
			StaleAt:          staleAt,
		}
		if err := repositories.Health.UpsertLocation(ctx, health); err != nil {
			t.Fatal(err)
		}
		claimed, err := repositories.Health.ClaimStale(
			ctx,
			health.MonitorID,
			health.LocationID,
			staleAt.Add(time.Second),
			staleAt.Add(time.Minute),
		)
		if err != nil || claimed {
			t.Fatalf("mismatched ClaimStale() = %v, %v", claimed, err)
		}
		claimed, err = repositories.Health.ClaimStale(
			ctx,
			health.MonitorID,
			health.LocationID,
			staleAt,
			staleAt.Add(time.Minute),
		)
		if err != nil || !claimed {
			t.Fatalf("ClaimStale() = %v, %v", claimed, err)
		}
		claimed, err = repositories.Health.ClaimStale(
			ctx,
			health.MonitorID,
			health.LocationID,
			staleAt,
			staleAt.Add(2*time.Minute),
		)
		if err != nil || claimed {
			t.Fatalf("repeated ClaimStale() = %v, %v", claimed, err)
		}

		return nil
	})
}

func testScheduleIdempotency(t *testing.T, store application.UnitOfWork) {
	t.Helper()
	ctx := context.Background()
	transact(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		fixture := seedWithoutRun(t, ctx, repositories, 1)
		run := application.NewRunRecord{
			ID:           runID,
			MonitorID:    fixture.monitor.ID,
			LocationID:   fixture.location.ID,
			ScheduledFor: fixture.now,
			Probe:        fixture.monitor.Probe(),
			Timeout:      fixture.monitor.Timeout,
		}
		inserted, err := repositories.Runs.Insert(ctx, run)
		if err != nil || !inserted {
			t.Fatalf("first Insert() = %v, %v", inserted, err)
		}
		run.ID = "00000000-0000-4000-8000-000000000008"
		inserted, err = repositories.Runs.Insert(ctx, run)
		if err != nil || inserted {
			t.Fatalf("duplicate schedule Insert() = %v, %v", inserted, err)
		}
		next := fixture.now.Add(time.Minute)
		advanced, err := repositories.Monitors.AdvanceNextRun(
			ctx,
			fixture.monitor.ID,
			next,
			next,
		)
		if err != nil || !advanced {
			t.Fatalf("first AdvanceNextRun() = %v, %v", advanced, err)
		}
		advanced, err = repositories.Monitors.AdvanceNextRun(
			ctx,
			fixture.monitor.ID,
			next,
			next,
		)
		if err != nil || advanced {
			t.Fatalf("repeated AdvanceNextRun() = %v, %v", advanced, err)
		}

		return nil
	})
}

type seeded struct {
	now      time.Time
	location domain.Location
	monitor  domain.Monitor
	agentIDs []domain.AgentID
	runID    domain.CheckRunID
}

func seed(
	t *testing.T,
	ctx context.Context,
	repositories application.Repositories,
	agentCount int,
) seeded {
	t.Helper()
	fixture := seedWithoutRun(t, ctx, repositories, agentCount)
	fixture.runID = runID
	inserted, err := repositories.Runs.Insert(
		ctx,
		application.NewRunRecord{
			ID:           fixture.runID,
			MonitorID:    fixture.monitor.ID,
			LocationID:   fixture.location.ID,
			ScheduledFor: fixture.now,
			Probe:        fixture.monitor.Probe(),
			Timeout:      fixture.monitor.Timeout,
		},
	)
	if err != nil || !inserted {
		t.Fatalf("seed run Insert() = %v, %v", inserted, err)
	}
	return fixture
}

func seedWithoutRun(
	t *testing.T,
	ctx context.Context,
	repositories application.Repositories,
	agentCount int,
) seeded {
	t.Helper()
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	location := mustLocation(t, locationID)
	if err := repositories.Locations.Create(ctx, location); err != nil {
		t.Fatal(err)
	}
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID:                monitorID,
		Name:              "website",
		Interval:          time.Minute,
		Timeout:           5 * time.Second,
		FailureThreshold:  1,
		RecoveryThreshold: 1,
		HTTP: domain.HTTPProbe{
			Method:         "GET",
			URL:            "https://example.test/health",
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
	if err := repositories.Monitors.AssignLocation(
		ctx,
		application.MonitorLocation{
			MonitorID:  monitor.ID,
			LocationID: location.ID,
			Required:   true,
		},
	); err != nil {
		t.Fatal(err)
	}

	agentIDs := make([]domain.AgentID, 0, agentCount)
	for index := range agentCount {
		id := domain.AgentID(fmt.Sprintf(
			"00000000-0000-4000-8001-%012d",
			index+1,
		))
		agent, err := domain.NewAgent(domain.NewAgentParams{
			ID:                   id,
			LocationID:           location.ID,
			Name:                 string(id),
			Capabilities:         []domain.AgentCapability{domain.CapabilityHTTP},
			CredentialGeneration: 1,
			CreatedAt:            now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := repositories.Agents.Create(ctx, application.AgentRecord{
			Agent:          agent,
			CredentialHash: []byte("credential-" + string(id)),
		}); err != nil {
			t.Fatal(err)
		}
		agentIDs = append(agentIDs, id)
	}
	return seeded{
		now: now, location: location, monitor: monitor, agentIDs: agentIDs,
	}
}

func mustLocation(t *testing.T, id domain.LocationID) domain.Location {
	t.Helper()
	location, err := domain.NewLocation(
		id,
		"homelab",
		time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	return location
}
