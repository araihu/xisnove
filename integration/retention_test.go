package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
	"github.com/araihu/xisnove/internal/adapters/database"
)

func TestRetentionUptimeStorageMatrix(t *testing.T) {
	t.Run("SQLite", func(t *testing.T) {
		runRetentionJourney(t, newFileStorageHarness(t, database.ProfileSQLite))
	})
	t.Run("TursoLocal", func(t *testing.T) {
		runRetentionJourney(t, newFileStorageHarness(t, database.ProfileTursoLocal))
	})
	t.Run("Postgres", func(t *testing.T) {
		runRetentionJourney(t, newPostgresStorageHarness(t))
	})
	t.Run("TursoCloud", func(t *testing.T) {
		runRetentionJourney(t, newTursoCloudStorageHarness(t))
	})
}

func runRetentionJourney(t *testing.T, harness *storageHarness) {
	t.Helper()
	ctx := context.Background()
	repositories := harness.primary.Store.Repositories()
	now, err := repositories.Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	location, err := domain.NewLocation("40000000-0000-4000-8000-000000000001", "retention", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Locations.Create(ctx, location); err != nil {
		t.Fatal(err)
	}
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID: "40000000-0000-4000-8000-000000000002", Name: "retention monitor",
		Interval: time.Minute, Timeout: 5 * time.Second,
		FailureThreshold: 1, RecoveryThreshold: 1,
		HTTP: domain.HTTPProbe{URL: "https://example.test/health"}, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Monitors.Create(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Monitors.AssignLocation(ctx, application.MonitorLocation{
		MonitorID: monitor.ID, LocationID: location.ID, Required: true,
	}); err != nil {
		t.Fatal(err)
	}
	agent, err := domain.NewAgent(domain.NewAgentParams{
		ID: "40000000-0000-4000-8000-000000000003", LocationID: location.ID,
		Name: "retention agent", Capabilities: []domain.AgentCapability{domain.CapabilityHTTP},
		CredentialGeneration: 1, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Agents.Create(ctx, application.AgentRecord{
		Agent: agent, CredentialHash: []byte("retention-matrix"),
	}); err != nil {
		t.Fatal(err)
	}
	insert := func(ordinal int, receivedAt time.Time, passed bool) {
		t.Helper()
		runID := domain.CheckRunID(fmt.Sprintf("40000000-0000-4000-8001-%012d", ordinal))
		inserted, err := repositories.Runs.Insert(ctx, application.NewRunRecord{
			ID: runID, MonitorID: monitor.ID, LocationID: location.ID,
			ScheduledFor: receivedAt.Add(-time.Second), Probe: monitor.Probe(), Timeout: monitor.Timeout,
		})
		if err != nil || !inserted {
			t.Fatalf("insert retention run %d = %t, %v", ordinal, inserted, err)
		}
		inserted, err = repositories.Results.Insert(ctx, application.ProbeResultRecord{
			ID:    fmt.Sprintf("40000000-0000-4000-8002-%012d", ordinal),
			RunID: runID, AgentID: agent.ID, StartedAt: receivedAt.Add(-time.Millisecond),
			FinishedAt: receivedAt, ReceivedAt: receivedAt, Passed: passed, Latency: time.Millisecond,
		})
		if err != nil || !inserted {
			t.Fatalf("insert retention result %d = %t, %v", ordinal, inserted, err)
		}
	}
	today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	for ordinal := 1; ordinal <= 3; ordinal++ {
		insert(ordinal, today.Add(time.Duration(ordinal)*time.Hour), ordinal != 2)
	}
	ids := &matrixIDs{}
	worker, err := application.NewRetentionWorker(application.RetentionWorkerConfig{
		Store: harness.secondary.Store, Tokens: &matrixTokens{}, NewID: ids.New,
		Owner: "retention-matrix", BatchSize: 2, LeaseDuration: time.Minute,
		PollInterval: time.Minute, RawRetention: 24 * time.Hour, DailyRetentionMonths: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	for page := 0; page < 3; page++ {
		if _, err := worker.RunOnce(ctx); err != nil {
			t.Fatalf("retention page %d: %v", page+1, err)
		}
	}
	daily, err := repositories.Retention.ListDailyUptime(ctx, monitor.ID, today, today.AddDate(0, 0, 1))
	if err != nil || len(daily) != 1 || daily[0].Passing != 2 || daily[0].Failing != 1 || daily[0].Observed != 3*time.Millisecond {
		t.Fatalf("daily uptime = %#v, %v", daily, err)
	}

	oldAt := now.Add(-25 * time.Hour)
	insert(4, oldAt, false)
	cycle, err := worker.RunOnce(ctx)
	if err != nil || cycle.ResultsDeleted != 1 {
		t.Fatalf("ordered aggregate and cleanup = %#v, %v", cycle, err)
	}
	if _, err := repositories.Results.GetByID(ctx, "40000000-0000-4000-8002-000000000004"); err == nil {
		t.Fatal("expired raw result remains after safe aggregation")
	}
	oldDay := time.Date(oldAt.UTC().Year(), oldAt.UTC().Month(), oldAt.UTC().Day(), 0, 0, 0, 0, time.UTC)
	daily, err = repositories.Retention.ListDailyUptime(ctx, monitor.ID, oldDay, oldDay.AddDate(0, 0, 1))
	if err != nil || len(daily) != 1 || daily[0].Failing != 1 {
		t.Fatalf("daily uptime after raw cleanup = %#v, %v", daily, err)
	}
}
