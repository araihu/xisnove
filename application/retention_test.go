package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

func TestRetentionWorkerResumesDailyAggregationAndRecomputesLateResults(t *testing.T) {
	ctx := context.Background()
	fixture := newProjectionFixture(t, ctx)
	now, err := fixture.repositories.Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	agentID := seedRetentionAgent(t, ctx, fixture, now)
	day := utcDay(now.Add(-24 * time.Hour))
	seedRetentionResult(t, ctx, fixture, agentID, 1, day.Add(time.Hour), true, 10*time.Millisecond)
	seedRetentionResult(t, ctx, fixture, agentID, 2, day.Add(2*time.Hour), false, 20*time.Millisecond)
	seedRetentionResult(t, ctx, fixture, agentID, 3, day.Add(3*time.Hour), true, 30*time.Millisecond)

	worker := newRetentionWorker(t, fixture.store, "retention-a", 2)
	if claimed, err := worker.AggregateOnce(ctx); err != nil || !claimed {
		t.Fatalf("first AggregateOnce() = %v, %v", claimed, err)
	}
	cursor := loadAggregationCursor(t, ctx, fixture)
	if cursor.Day != day.Format(time.DateOnly) || cursor.AfterID != retentionID("result", 2) || len(cursor.ByMonitor) != 1 {
		t.Fatalf("partial cursor = %#v", cursor)
	}
	if daily := listRetentionDaily(t, ctx, fixture, day); len(daily) != 0 {
		t.Fatalf("partial aggregation was published: %#v", daily)
	}

	// A fresh worker owner proves that partial progress lives in storage, not memory.
	worker = newRetentionWorker(t, fixture.store, "retention-b", 2)
	if claimed, err := worker.AggregateOnce(ctx); err != nil || !claimed {
		t.Fatalf("resumed AggregateOnce() = %v, %v", claimed, err)
	}
	daily := listRetentionDaily(t, ctx, fixture, day)
	if len(daily) != 1 || daily[0].Passing != 2 || daily[0].Failing != 1 || daily[0].Observed != 60*time.Millisecond {
		t.Fatalf("daily uptime = %#v", daily)
	}

	// Finish today's empty bucket so the durable cursor wraps to the raw window.
	if _, err := worker.AggregateOnce(ctx); err != nil {
		t.Fatal(err)
	}
	seedRetentionResult(t, ctx, fixture, agentID, 4, day.Add(4*time.Hour), false, 40*time.Millisecond)
	// Four rows at a page size of two require an empty terminal page before publish.
	for index := 0; index < 3; index++ {
		if _, err := worker.AggregateOnce(ctx); err != nil {
			t.Fatalf("late aggregation page %d: %v", index+1, err)
		}
	}
	daily = listRetentionDaily(t, ctx, fixture, day)
	if len(daily) != 1 || daily[0].Passing != 2 || daily[0].Failing != 2 || daily[0].Observed != 100*time.Millisecond {
		t.Fatalf("idempotent late-result recomputation = %#v", daily)
	}
	var aggregationAudits int
	if err := fixture.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE kind = 'retention.uptime-aggregated'`).Scan(&aggregationAudits); err != nil || aggregationAudits != 2 {
		t.Fatalf("aggregation audits = %d, %v", aggregationAudits, err)
	}
}

func TestRetentionWorkerPrunesBoundedHistoryAndKeepsCutoff(t *testing.T) {
	ctx := context.Background()
	fixture := newProjectionFixture(t, ctx)
	now, err := fixture.repositories.Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	agentID := seedRetentionAgent(t, ctx, fixture, now)
	cutoff := now.Add(-24 * time.Hour)
	seedRetentionResult(t, ctx, fixture, agentID, 10, cutoff.Add(-2*time.Second), true, time.Millisecond)
	seedRetentionResult(t, ctx, fixture, agentID, 11, cutoff.Add(-time.Second), true, time.Millisecond)
	seedRetentionResult(t, ctx, fixture, agentID, 12, cutoff.Add(time.Second), true, time.Millisecond)

	dailyCutoff := utcDay(now.AddDate(0, -1, 0))
	for index, day := range []time.Time{dailyCutoff.AddDate(0, 0, -2), dailyCutoff.AddDate(0, 0, -1), dailyCutoff} {
		if err := fixture.repositories.Retention.UpsertDailyUptime(ctx, port.DailyUptimeRecord{
			MonitorID: fixture.monitor.ID, Day: day, Passing: uint64(index + 1), UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	worker := newRetentionWorkerWithConfig(t, RetentionWorkerConfig{
		Store: fixture.store, Tokens: &workerTokenIssuer{}, NewID: sequentialRetentionIDs(),
		Owner: "cleanup", BatchSize: 1, LeaseDuration: time.Second,
		PollInterval: time.Millisecond, RawRetention: 24 * time.Hour, DailyRetentionMonths: 1,
	})
	for cycle := 0; cycle < 2; cycle++ {
		deleted, err := worker.cleanupOnce(ctx, resultCleanupLeaseKey, true)
		if err != nil || deleted != 1 {
			t.Fatalf("result cleanup %d = %d, %v", cycle+1, deleted, err)
		}
		deleted, err = worker.cleanupOnce(ctx, dailyCleanupLeaseKey, false)
		if err != nil || deleted != 1 {
			t.Fatalf("daily cleanup %d = %d, %v", cycle+1, deleted, err)
		}
	}
	if _, err := fixture.repositories.Results.GetByID(ctx, retentionID("result", 12)); err != nil {
		t.Fatalf("result after cutoff was removed: %v", err)
	}
	daily := listRetentionDaily(t, ctx, fixture, dailyCutoff)
	if len(daily) != 1 || !daily[0].Day.Equal(dailyCutoff) {
		t.Fatalf("daily row at exact cutoff = %#v", daily)
	}
	var resultAudits, dailyAudits int
	if err := fixture.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE kind = 'retention.results-pruned'`).Scan(&resultAudits); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE kind = 'retention.daily-pruned'`).Scan(&dailyAudits); err != nil {
		t.Fatal(err)
	}
	if resultAudits != 2 || dailyAudits != 2 {
		t.Fatalf("cleanup audits = results %d, daily %d", resultAudits, dailyAudits)
	}
}

func TestRetentionWorkerRollsBackCleanupWhenAuditFails(t *testing.T) {
	ctx := context.Background()
	fixture := newProjectionFixture(t, ctx)
	now, err := fixture.repositories.Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	agentID := seedRetentionAgent(t, ctx, fixture, now)
	seedRetentionResult(t, ctx, fixture, agentID, 20, now.Add(-25*time.Hour), true, time.Millisecond)
	if _, err := fixture.db.ExecContext(ctx, `
		CREATE TRIGGER fail_retention_audit BEFORE INSERT ON audit_events
		WHEN NEW.kind = 'retention.results-pruned'
		BEGIN SELECT RAISE(FAIL, 'injected retention audit failure'); END
	`); err != nil {
		t.Fatal(err)
	}
	worker := newRetentionWorker(t, fixture.store, "rollback", 1)
	if deleted, err := worker.cleanupOnce(ctx, resultCleanupLeaseKey, true); err == nil || deleted != 1 {
		t.Fatalf("cleanup with audit failure = %d, %v", deleted, err)
	}
	if _, err := fixture.repositories.Results.GetByID(ctx, retentionID("result", 20)); err != nil {
		t.Fatalf("result deletion was not rolled back: %v", err)
	}
	if _, err := fixture.db.ExecContext(ctx, `DROP TRIGGER fail_retention_audit`); err != nil {
		t.Fatal(err)
	}
	if deleted, err := worker.cleanupOnce(ctx, resultCleanupLeaseKey, true); err != nil || deleted != 1 {
		t.Fatalf("cleanup after rollback = %d, %v", deleted, err)
	}
}

func TestRetentionWorkerDoesNotPruneRawResultsWithoutAggregationLease(t *testing.T) {
	ctx := context.Background()
	fixture := newProjectionFixture(t, ctx)
	now, err := fixture.repositories.Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	agentID := seedRetentionAgent(t, ctx, fixture, now)
	seedRetentionResult(t, ctx, fixture, agentID, 30, now.Add(-25*time.Hour), true, time.Millisecond)
	cursor, err := json.Marshal(newAggregationCursor(utcDay(now.Add(-24 * time.Hour))))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repositories.Retention.ClaimLease(ctx, port.OperationLeaseRecord{
		Key: dailyAggregationLeaseKey, Owner: "other-replica", TokenHash: []byte("held"),
		ExpiresAt: now.Add(time.Hour), Cursor: cursor, UpdatedAt: now,
	}, now); err != nil {
		t.Fatal(err)
	}
	worker := newRetentionWorker(t, fixture.store, "blocked", 1)
	result, err := worker.RunOnce(ctx)
	if err != nil || result.AggregationClaimed || result.ResultsDeleted != 0 {
		t.Fatalf("RunOnce() while aggregation is held = %#v, %v", result, err)
	}
	if _, err := fixture.repositories.Results.GetByID(ctx, retentionID("result", 30)); err != nil {
		t.Fatalf("unaggregated raw result was removed: %v", err)
	}
}

func TestRetentionWorkerStopsCleanly(t *testing.T) {
	fixture := newProjectionFixture(t, context.Background())
	worker := newRetentionWorker(t, fixture.store, "stop", 2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() = %v", err)
	}
}

func newRetentionWorker(t *testing.T, store port.UnitOfWork, owner string, batch int) *RetentionWorker {
	t.Helper()
	return newRetentionWorkerWithConfig(t, RetentionWorkerConfig{
		Store: store, Tokens: &workerTokenIssuer{}, NewID: sequentialRetentionIDs(), Owner: owner,
		BatchSize: batch, LeaseDuration: time.Second, PollInterval: time.Millisecond,
		RawRetention: 24 * time.Hour, DailyRetentionMonths: 1,
	})
}

func newRetentionWorkerWithConfig(t *testing.T, config RetentionWorkerConfig) *RetentionWorker {
	t.Helper()
	worker, err := NewRetentionWorker(config)
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func seedRetentionAgent(t *testing.T, ctx context.Context, fixture projectionFixture, now time.Time) domain.AgentID {
	t.Helper()
	agent, err := domain.NewAgent(domain.NewAgentParams{
		ID: "00000000-0000-4000-8000-000000000090", LocationID: fixture.location.ID,
		Name: "retention-agent", Capabilities: []domain.AgentCapability{domain.CapabilityHTTP},
		CredentialGeneration: 1, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.repositories.Agents.Create(ctx, port.AgentRecord{Agent: agent, CredentialHash: []byte("retention-agent")}); err != nil {
		t.Fatal(err)
	}
	return agent.ID
}

func seedRetentionResult(t *testing.T, ctx context.Context, fixture projectionFixture, agentID domain.AgentID, ordinal int, receivedAt time.Time, passed bool, latency time.Duration) {
	t.Helper()
	runID := domain.CheckRunID(retentionID("run", ordinal))
	inserted, err := fixture.repositories.Runs.Insert(ctx, port.NewRunRecord{
		ID: runID, MonitorID: fixture.monitor.ID, LocationID: fixture.location.ID,
		ScheduledFor: receivedAt.Add(-time.Second), Probe: fixture.monitor.Probe(), Timeout: fixture.monitor.Timeout,
	})
	if err != nil || !inserted {
		t.Fatalf("insert run %d = %v, %v", ordinal, inserted, err)
	}
	inserted, err = fixture.repositories.Results.Insert(ctx, port.ProbeResultRecord{
		ID: retentionID("result", ordinal), RunID: runID, AgentID: agentID,
		StartedAt: receivedAt.Add(-latency), FinishedAt: receivedAt,
		ReceivedAt: receivedAt, Passed: passed, Latency: latency,
	})
	if err != nil || !inserted {
		t.Fatalf("insert result %d = %v, %v", ordinal, inserted, err)
	}
}

func retentionID(kind string, ordinal int) string {
	prefix := 1
	if kind == "result" {
		prefix = 2
	}
	return fmt.Sprintf("%08d-0000-4000-8000-%012d", prefix, ordinal)
}

func sequentialRetentionIDs() func() string {
	next := 0
	return func() string {
		next++
		return fmt.Sprintf("30000000-0000-4000-8000-%012d", next)
	}
}

func loadAggregationCursor(t *testing.T, ctx context.Context, fixture projectionFixture) aggregationCursor {
	t.Helper()
	var raw []byte
	if err := fixture.db.QueryRowContext(ctx, `SELECT cursor_json FROM operation_leases WHERE lease_key = ?`, dailyAggregationLeaseKey).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var cursor aggregationCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		t.Fatal(err)
	}
	return cursor
}

func listRetentionDaily(t *testing.T, ctx context.Context, fixture projectionFixture, day time.Time) []port.DailyUptimeRecord {
	t.Helper()
	records, err := fixture.repositories.Retention.ListDailyUptime(ctx, fixture.monitor.ID, day, day.AddDate(0, 0, 1))
	if err != nil && !errors.Is(err, port.ErrNotFound) {
		t.Fatal(err)
	}
	return records
}
