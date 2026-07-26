package migrations_test

import (
	"io/fs"
	"strings"
	"testing"

	migrations "github.com/araihu/xisnove/db/migrations/sqlite"
)

func TestMigrationFamilyContainsNotificationOperationsSchema(t *testing.T) {
	t.Parallel()

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
		"description TEXT NOT NULL DEFAULT ''",
		"labels_json BLOB NOT NULL DEFAULT X'7B7D';",
		"action TEXT NOT NULL DEFAULT 'change'",
	} {
		if !strings.Contains(schema.String(), expected) {
			t.Fatalf("migration family is missing %q", expected)
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
		"CREATE TABLE api_tokens", "token_hash BLOB NOT NULL UNIQUE",
		"scopes_json BLOB NOT NULL", "api_tokens_created_id",
		"last_used_at", "revoked_at",
	} {
		if !strings.Contains(schema.String(), expected) {
			t.Fatalf("human-client auth schema is missing %q", expected)
		}
	}
}
