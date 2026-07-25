package postgres_test

import (
	"io/fs"
	"strings"
	"testing"

	migrations "github.com/araihu/xisnove/db/migrations/postgres"
)

func TestMigrationFamilyContainsNativeCurrentSchema(t *testing.T) {
	t.Parallel()

	if migrations.LatestVersion != 3 {
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
	} {
		if !strings.Contains(schema.String(), expected) {
			t.Fatalf("migration family is missing %q", expected)
		}
	}
}
