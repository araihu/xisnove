package postgres_test

import (
	"io/fs"
	"strings"
	"testing"

	migrations "github.com/araihu/xisnove/db/migrations/postgres"
)

func TestMigrationFamilyContainsNativeCurrentSchema(t *testing.T) {
	t.Parallel()

	if migrations.LatestVersion != 6 {
		t.Fatalf("LatestVersion = %d", migrations.LatestVersion)
	}
	var schema strings.Builder
	err := fs.WalkDir(migrations.Files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".sql") {
			return nil
		}
		content, err := migrations.Files.ReadFile(path)
		if err != nil {
			return err
		}
		schema.Write(content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"id UUID PRIMARY KEY",
		"TIMESTAMPTZ",
		"JSONB",
		"one_active_incident_per_monitor",
		"monitor_schedule",
		"due_location_health",
		"probe_kind",
		"notification_channels",
		"notification_routes",
		"notification_outbox",
		"notification_delivery_attempts",
		"maintenance_intervals",
		"audit_events",
		"daily_uptime",
		"operation_leases",
		"UNIQUE (incident_event_id, route_id, channel_id)",
		"due_notification_outbox",
		"due_maintenance_end",
		"probe_results_received_at",
		"labels_json JSONB NOT NULL DEFAULT '{}'::jsonb",
		"action TEXT NOT NULL DEFAULT 'change'",
	} {
		if !strings.Contains(schema.String(), expected) {
			t.Fatalf("migration family is missing %q", expected)
		}
	}
}

func TestIdempotencySchema(t *testing.T) {
	t.Parallel()

	content, err := migrations.Files.ReadFile("00006_idempotency.sql")
	if err != nil {
		t.Fatal(err)
	}
	schema := string(content)
	for _, expected := range []string{
		"CREATE TABLE idempotency_records",
		"PRIMARY KEY (principal_id, operation_id, idempotency_key)",
		"idempotency_records_expiry",
	} {
		if !strings.Contains(schema, expected) {
			t.Fatalf("idempotency schema is missing %q", expected)
		}
	}
}

func TestHumanClientAuthSchema(t *testing.T) {
	t.Parallel()

	var schema strings.Builder
	err := fs.WalkDir(migrations.Files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".sql") {
			return err
		}
		content, err := migrations.Files.ReadFile(path)
		if err == nil {
			schema.Write(content)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"CREATE TABLE api_tokens", "token_hash BYTEA NOT NULL UNIQUE",
		"scopes_json JSONB NOT NULL", "api_tokens_created_id",
		"last_used_at", "revoked_at",
	} {
		if !strings.Contains(schema.String(), expected) {
			t.Fatalf("human-client auth schema is missing %q", expected)
		}
	}
}
