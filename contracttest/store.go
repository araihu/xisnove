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

type Factory func(*testing.T) application.Store

const (
	locationID = domain.LocationID("00000000-0000-4000-8000-000000000001")
	monitorID  = domain.MonitorID("00000000-0000-4000-8000-000000000002")
	runID      = domain.CheckRunID("00000000-0000-4000-8000-000000000003")
)

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

func testTransactionRollback(t *testing.T, store application.Store) {
	t.Helper()
	ctx := context.Background()
	stop := errors.New("stop")

	err := store.WithinTx(ctx, func(repositories application.Repositories) error {
		location := mustLocation(t, locationID)
		if err := repositories.Locations.Create(ctx, location); err != nil {
			return err
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("WithinTx() error = %v, want %v", err, stop)
	}
	_, err = store.Repositories().Locations.Get(ctx, locationID)
	if !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

func testDuplicateResult(t *testing.T, store application.Store) {
	t.Helper()
	ctx := context.Background()
	fixture := seed(t, store, 1)
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
	inserted, err := store.Repositories().Results.Insert(ctx, result)
	if err != nil || !inserted {
		t.Fatalf("first Insert() = %v, %v", inserted, err)
	}
	inserted, err = store.Repositories().Results.Insert(ctx, result)
	if err != nil || inserted {
		t.Fatalf("duplicate ID Insert() = %v, %v", inserted, err)
	}
	result.ID = "00000000-0000-4000-8000-000000000005"
	inserted, err = store.Repositories().Results.Insert(ctx, result)
	if err != nil || inserted {
		t.Fatalf("duplicate run Insert() = %v, %v", inserted, err)
	}
}

func testOneActiveIncident(t *testing.T, store application.Store) {
	t.Helper()
	ctx := context.Background()
	fixture := seed(t, store, 1)
	first := domain.Incident{
		ID:               "00000000-0000-4000-8000-000000000006",
		MonitorID:        fixture.monitor.ID,
		State:            domain.HealthDown,
		Severity:         domain.IncidentCritical,
		OpenedAt:         fixture.now,
		LastTransitionAt: fixture.now,
	}
	if err := store.Repositories().Incidents.Open(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID = "00000000-0000-4000-8000-000000000007"
	err := store.Repositories().Incidents.Open(ctx, second)
	if !errors.Is(err, application.ErrConflict) {
		t.Fatalf("second Open() error = %v, want ErrConflict", err)
	}
}

func testCompetingAndExpiredLease(t *testing.T, store application.Store) {
	t.Helper()
	ctx := context.Background()
	fixture := seed(t, store, 2)
	start := make(chan struct{})
	records := make([]application.RunRecord, 2)
	errs := make([]error, 2)
	var group sync.WaitGroup
	for index, agentID := range fixture.agentIDs {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			records[index], errs[index] = store.Repositories().Runs.ClaimProbe(
				ctx,
				application.ClaimRunParams{
					AgentID:        agentID,
					Capabilities:   []domain.AgentCapability{domain.CapabilityHTTP},
					LeaseTokenHash: []byte(fmt.Sprintf("lease-%d", index)),
					LeaseExpiresAt: fixture.now.Add(30 * time.Second),
					Now:            fixture.now,
				},
			)
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
	reclaimed, err := store.Repositories().Runs.ClaimProbe(
		ctx,
		application.ClaimRunParams{
			AgentID:        fixture.agentIDs[loser],
			Capabilities:   []domain.AgentCapability{domain.CapabilityHTTP},
			LeaseTokenHash: []byte("replacement-lease"),
			LeaseExpiresAt: fixture.now.Add(2 * time.Minute),
			Now:            fixture.now.Add(time.Minute),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed.ID != fixture.runID || reclaimed.LeaseAttempt != 2 {
		t.Fatalf("reclaimed run = %#v", reclaimed)
	}
}

func testStaleCompareAndSet(t *testing.T, store application.Store) {
	t.Helper()
	ctx := context.Background()
	fixture := seed(t, store, 1)
	staleAt := fixture.now.Add(time.Minute)
	health := domain.LocationHealth{
		MonitorID:        fixture.monitor.ID,
		LocationID:       fixture.location.ID,
		State:            domain.HealthUp,
		LastObservedAt:   fixture.now,
		LastTransitionAt: fixture.now,
		StaleAt:          staleAt,
	}
	if err := store.Repositories().Health.UpsertLocation(ctx, health); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Repositories().Health.ClaimStale(
		ctx,
		health.MonitorID,
		health.LocationID,
		staleAt.Add(time.Second),
		staleAt.Add(time.Minute),
	)
	if err != nil || claimed {
		t.Fatalf("mismatched ClaimStale() = %v, %v", claimed, err)
	}
	claimed, err = store.Repositories().Health.ClaimStale(
		ctx,
		health.MonitorID,
		health.LocationID,
		staleAt,
		staleAt.Add(time.Minute),
	)
	if err != nil || !claimed {
		t.Fatalf("ClaimStale() = %v, %v", claimed, err)
	}
	claimed, err = store.Repositories().Health.ClaimStale(
		ctx,
		health.MonitorID,
		health.LocationID,
		staleAt,
		staleAt.Add(2*time.Minute),
	)
	if err != nil || claimed {
		t.Fatalf("repeated ClaimStale() = %v, %v", claimed, err)
	}
}

func testScheduleIdempotency(t *testing.T, store application.Store) {
	t.Helper()
	ctx := context.Background()
	fixture := seedWithoutRun(t, store, 1)
	run := application.NewRunRecord{
		ID:           runID,
		MonitorID:    fixture.monitor.ID,
		LocationID:   fixture.location.ID,
		ScheduledFor: fixture.now,
		Probe:        fixture.monitor.Probe(),
		Timeout:      fixture.monitor.Timeout,
	}
	inserted, err := store.Repositories().Runs.Insert(ctx, run)
	if err != nil || !inserted {
		t.Fatalf("first Insert() = %v, %v", inserted, err)
	}
	run.ID = "00000000-0000-4000-8000-000000000008"
	inserted, err = store.Repositories().Runs.Insert(ctx, run)
	if err != nil || inserted {
		t.Fatalf("duplicate schedule Insert() = %v, %v", inserted, err)
	}
	next := fixture.now.Add(time.Minute)
	advanced, err := store.Repositories().Monitors.AdvanceNextRun(
		ctx,
		fixture.monitor.ID,
		next,
		next,
	)
	if err != nil || !advanced {
		t.Fatalf("first AdvanceNextRun() = %v, %v", advanced, err)
	}
	advanced, err = store.Repositories().Monitors.AdvanceNextRun(
		ctx,
		fixture.monitor.ID,
		next,
		next,
	)
	if err != nil || advanced {
		t.Fatalf("repeated AdvanceNextRun() = %v, %v", advanced, err)
	}
}

type seeded struct {
	now      time.Time
	location domain.Location
	monitor  domain.Monitor
	agentIDs []domain.AgentID
	runID    domain.CheckRunID
}

func seed(t *testing.T, store application.Store, agentCount int) seeded {
	t.Helper()
	fixture := seedWithoutRun(t, store, agentCount)
	fixture.runID = runID
	inserted, err := store.Repositories().Runs.Insert(
		context.Background(),
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

func seedWithoutRun(t *testing.T, store application.Store, agentCount int) seeded {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	location := mustLocation(t, locationID)
	if err := store.Repositories().Locations.Create(ctx, location); err != nil {
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
	if err := store.Repositories().Monitors.Create(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	if err := store.Repositories().Monitors.AssignLocation(
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
		if err := store.Repositories().Agents.Create(ctx, application.AgentRecord{
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
