package integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/araihu/xisnove/internal/adapters/backup"
	"github.com/araihu/xisnove/internal/adapters/database"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestBackupRestorePreservesFirstObservationState(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.db")
	backupPath := filepath.Join(directory, "backup.db")
	source, err := sqlitestore.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	if err := sqlitestore.Migrate(ctx, source); err != nil {
		t.Fatal(err)
	}
	seedBackupState(t, source)

	reader, err := sqlitestore.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	stopReads := make(chan struct{})
	readErrors := make(chan error, 1)
	firstRead := make(chan struct{})
	var reads atomic.Int64
	go func() {
		first := true
		for {
			var count int
			if err := reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM admins").Scan(&count); err != nil {
				readErrors <- err
				return
			}
			reads.Add(1)
			if first {
				close(firstRead)
				first = false
			}
			select {
			case <-stopReads:
				readErrors <- nil
				return
			default:
			}
		}
	}()
	<-firstRead
	if err := backup.Create(ctx, database.ProfileSQLite, source, backupPath); err != nil {
		close(stopReads)
		<-readErrors
		t.Fatal(err)
	}
	close(stopReads)
	if err := <-readErrors; err != nil {
		t.Fatalf("read during online backup: %v", err)
	}
	if reads.Load() < 2 {
		t.Fatalf("reads completed during backup = %d, want at least 2", reads.Load())
	}

	restored, err := sqlitestore.Open(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	if err := sqlitestore.Ready(ctx, restored); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{
		"admins", "monitors", "probe_results", "location_health",
		"monitor_health", "incidents", "incident_events",
	} {
		if got := rowCount(t, restored, table); got != 1 {
			t.Fatalf("restored %s rows = %d, want 1", table, got)
		}
	}
	for table, id := range map[string]string{
		"admins":        "00000000-0000-4000-8000-000000000001",
		"monitors":      "00000000-0000-4000-8000-000000000003",
		"probe_results": "00000000-0000-4000-8000-000000000007",
		"incidents":     "00000000-0000-4000-8000-000000000008",
	} {
		var got string
		if err := restored.QueryRowContext(ctx, "SELECT id FROM "+table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != id {
			t.Fatalf("restored %s id = %q, want %q", table, got, id)
		}
	}
}

func seedBackupState(t *testing.T, db *sql.DB) {
	t.Helper()
	statements := []string{
		`INSERT INTO admins (id, email, password_hash, created_at) VALUES ('00000000-0000-4000-8000-000000000001', 'admin@example.com', 'hash', '2026-07-25T00:00:00Z')`,
		`INSERT INTO locations (id, name, created_at) VALUES ('00000000-0000-4000-8000-000000000002', 'public', '2026-07-25T00:00:00Z')`,
		`INSERT INTO monitors (id, name, kind, interval_ms, timeout_ms, failure_threshold, recovery_threshold, probe_json, enabled, next_run_at, created_at, updated_at) VALUES ('00000000-0000-4000-8000-000000000003', 'target', 'http', 60000, 5000, 3, 2, '{"kind":"http","method":"GET","url":"https://example.com"}', 1, '2026-07-25T00:01:00Z', '2026-07-25T00:00:00Z', '2026-07-25T00:00:00Z')`,
		`INSERT INTO monitor_locations (monitor_id, location_id, required) VALUES ('00000000-0000-4000-8000-000000000003', '00000000-0000-4000-8000-000000000002', 1)`,
		`INSERT INTO agents (id, location_id, name, credential_hash, credential_generation, capabilities_json, version, last_seen_at, created_at) VALUES ('00000000-0000-4000-8000-000000000004', '00000000-0000-4000-8000-000000000002', 'agent', X'01', 1, '["http"]', 'v1', '2026-07-25T00:00:00Z', '2026-07-25T00:00:00Z')`,
		`INSERT INTO check_runs (id, monitor_id, location_id, scheduled_for, probe_json, probe_kind, timeout_ms, status, lease_agent_id, lease_token_hash, lease_attempt, lease_expires_at, resolved_at) VALUES ('00000000-0000-4000-8000-000000000005', '00000000-0000-4000-8000-000000000003', '00000000-0000-4000-8000-000000000002', '2026-07-25T00:00:00Z', '{"kind":"http"}', 'http', 5000, 'resolved', '00000000-0000-4000-8000-000000000004', X'02', 1, '2026-07-25T00:01:00Z', '2026-07-25T00:00:01Z')`,
		`INSERT INTO probe_results (id, run_id, agent_id, started_at, finished_at, received_at, outcome, latency_ms, observed_status, body_assertion_passed, error_code, diagnostic_sample, observed_values_json, protocol_timings_json) VALUES ('00000000-0000-4000-8000-000000000007', '00000000-0000-4000-8000-000000000005', '00000000-0000-4000-8000-000000000004', '2026-07-25T00:00:00Z', '2026-07-25T00:00:01Z', '2026-07-25T00:00:01Z', 'failed', 1000, 503, 0, 'status_mismatch', 'HTTP 503', '[]', '{}')`,
		`INSERT INTO location_health (monitor_id, location_id, state, consecutive_failures, consecutive_successes, last_observed_at, last_transition_at, stale_at) VALUES ('00000000-0000-4000-8000-000000000003', '00000000-0000-4000-8000-000000000002', 'down', 3, 0, '2026-07-25T00:00:01Z', '2026-07-25T00:00:01Z', '2026-07-25T00:02:00Z')`,
		`INSERT INTO monitor_health (monitor_id, state, last_transition_at) VALUES ('00000000-0000-4000-8000-000000000003', 'down', '2026-07-25T00:00:01Z')`,
		`INSERT INTO incidents (id, monitor_id, state, severity, opened_at, last_transition_at) VALUES ('00000000-0000-4000-8000-000000000008', '00000000-0000-4000-8000-000000000003', 'down', 'critical', '2026-07-25T00:00:01Z', '2026-07-25T00:00:01Z')`,
		`INSERT INTO incident_events (id, incident_id, previous_state, state, severity, created_at) VALUES ('00000000-0000-4000-8000-000000000009', '00000000-0000-4000-8000-000000000008', 'unknown', 'down', 'critical', '2026-07-25T00:00:01Z')`,
		`CREATE TABLE backup_padding (payload BLOB NOT NULL)`,
		`WITH RECURSIVE rows(value) AS (SELECT 1 UNION ALL SELECT value + 1 FROM rows WHERE value < 2000) INSERT INTO backup_padding SELECT randomblob(4096) FROM rows`,
	}
	for index, statement := range statements {
		if _, err := db.ExecContext(context.Background(), statement); err != nil {
			t.Fatalf("seed backup statement %d: %v", index, err)
		}
	}
}

func rowCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(context.Background(), fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
