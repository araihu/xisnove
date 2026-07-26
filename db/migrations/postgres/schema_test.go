package postgres_test

import (
	"io/fs"
	"strings"
	"testing"

	migrations "github.com/araihu/xisnove/db/migrations/postgres"
)

func TestMigrationFamilyContainsNativeCurrentSchema(t *testing.T) {
	t.Parallel()

	if migrations.LatestVersion != 10 {
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

func TestDiscoverySchema(t *testing.T) {
	t.Parallel()
	content, err := migrations.Files.ReadFile("00009_discovery.sql")
	if err != nil {
		t.Fatal(err)
	}
	schema := string(content)
	for _, expected := range []string{
		"CREATE TABLE discovery_batches", "CREATE TABLE discovery_candidates",
		"UNIQUE (agent_id, location_id, source_kind, source_uid, protocol, target)",
		"ON DELETE SET NULL", "discovery_candidates_present_updated_id",
		"discovery_candidates_location_updated_id",
	} {
		if !strings.Contains(schema, expected) {
			t.Fatalf("discovery schema is missing %q", expected)
		}
	}
}

func TestLocationLifecycleSchema(t *testing.T) {
	t.Parallel()

	content, err := migrations.Files.ReadFile("00008_location_lifecycle.sql")
	if err != nil {
		t.Fatal(err)
	}
	schema := string(content)
	for _, expected := range []string{
		"ADD COLUMN enabled BOOLEAN NOT NULL DEFAULT TRUE",
		"ADD COLUMN updated_at TIMESTAMPTZ",
		"UPDATE locations SET updated_at = created_at",
		"ALTER COLUMN updated_at SET NOT NULL",
		"DROP COLUMN updated_at",
		"DROP COLUMN enabled",
	} {
		if !strings.Contains(schema, expected) {
			t.Fatalf("location lifecycle migration is missing %q", expected)
		}
	}
}

func TestAgentCredentialGenerationSchema(t *testing.T) {
	t.Parallel()

	content, err := migrations.Files.ReadFile("00007_agent_credentials.sql")
	if err != nil {
		t.Fatal(err)
	}
	schema := string(content)
	for _, expected := range []string{
		"CREATE TABLE agent_credentials",
		"PRIMARY KEY (agent_id, generation)",
		"credential_hash BYTEA NOT NULL UNIQUE",
		"generation BIGINT NOT NULL CHECK (generation > 0)",
		"REFERENCES agents(id) ON DELETE CASCADE",
		"last_authenticated_at TIMESTAMPTZ",
		"ALTER TABLE agents ADD COLUMN updated_at TIMESTAMPTZ",
		"SET updated_at = GREATEST",
		"ALTER COLUMN updated_at SET NOT NULL",
		"locations_name_id",
		"monitors_display_order_id",
		"agents_name_id",
		"incidents_opened_id",
		"incident_events_incident_created_id",
		"SELECT id, credential_generation, credential_hash, created_at, revoked_at, last_seen_at",
	} {
		if !strings.Contains(schema, expected) {
			t.Fatalf("agent credential migration is missing %q", expected)
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
