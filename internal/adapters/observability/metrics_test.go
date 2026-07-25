package observability

import (
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMetricsExposeRequiredBoundedMeasures(t *testing.T) {
	m := NewMetrics()
	m.SetMonitorState(MonitorPassing, 2)
	m.ObserveTransition(MonitorFailing, MonitorPassing)
	m.ObserveProbe(ProbeSuccess, 50*time.Millisecond)
	m.ObserveSchedulerCycle(CycleSuccess)
	m.SetLeases(LeaseClaimed, 1)
	m.ObserveLeaseEvent(LeaseExpired)
	m.SetHeartbeatAge(time.Minute)
	m.ObserveDuplicate(DuplicateResult)
	m.SetOutboxOldestAge(OutboxRetry, time.Minute)
	m.ObserveAttempt(AttemptRetry, DiagnosticTimeout)
	m.SetPool(PoolDatabase, PoolInUse, 3)
	m.ObserveTransaction(TransactionTransact, TransactionCommit, time.Millisecond)
	m.ObserveMigration(MigrationSuccess)
	m.SetSchemaVersion(4)

	request := httptest.NewRequest("GET", "/metrics", nil)
	response := httptest.NewRecorder()
	m.Handler().ServeHTTP(response, request)
	body := response.Body.String()
	for _, name := range []string{
		"xisnove_monitor_state", "xisnove_monitor_transitions_total", "xisnove_probe_duration_seconds",
		"xisnove_scheduler_cycles_total", "xisnove_leases", "xisnove_lease_events_total", "xisnove_agent_heartbeat_age_seconds",
		"xisnove_duplicates_total", "xisnove_notification_outbox_oldest_age_seconds",
		"xisnove_notification_attempts_total", "xisnove_pool_connections",
		"xisnove_transaction_duration_seconds", "xisnove_migration_runs_total", "xisnove_schema_version",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("metrics output missing %s", name)
		}
	}
	for _, forbidden := range []string{"monitor_name", "monitor_id", "url=", "error=", "token="} {
		if strings.Contains(body, forbidden) {
			t.Errorf("metrics output contains forbidden label %q", forbidden)
		}
	}
}

func TestMetricsLeaveUnpopulatedAggregateFamiliesUnset(t *testing.T) {
	m := NewMetrics()
	response := httptest.NewRecorder()
	m.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	body := response.Body.String()
	for _, name := range []string{
		"xisnove_monitor_state",
		"xisnove_leases",
		"xisnove_agent_heartbeat_age_seconds",
		"xisnove_notification_outbox_oldest_age_seconds",
		"xisnove_schema_version",
	} {
		if strings.Contains(body, name) {
			t.Errorf("unpopulated metrics output contains misleading family %s", name)
		}
	}
}

func TestMetricsFoldUnknownLabelsIntoOtherAndAreConcurrentSafe(t *testing.T) {
	m := NewMetrics()
	const secret = "https://user:token@example.test/a-unique-monitor"
	var workers sync.WaitGroup
	for range 20 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 100 {
				m.SetMonitorState(MonitorState(secret), 1)
				m.ObserveAttempt(AttemptOutcome(secret), DiagnosticClass(secret))
			}
		}()
	}
	workers.Wait()
	response := httptest.NewRecorder()
	m.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	body, err := io.ReadAll(response.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), secret) {
		t.Fatal("unbounded input leaked into metric labels")
	}
	if !strings.Contains(string(body), `state="other"`) || !strings.Contains(string(body), `outcome="other"`) {
		t.Fatal("unknown labels were not folded into the bounded other bucket")
	}
}
