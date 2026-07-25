package tursocloud

import (
	"strings"
	"testing"
)

func TestUpMigrationExtractsOnlyUpSection(t *testing.T) {
	t.Parallel()

	got, err := upMigration(`-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE example (id INTEGER);
-- +goose Down
DROP TABLE example;`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "CREATE TABLE example") {
		t.Fatalf("up migration = %q", got)
	}
	if strings.Contains(got, "DROP TABLE") {
		t.Fatalf("up migration includes down section: %q", got)
	}
}

func TestUpMigrationRejectsMissingOrEmptyUpSection(t *testing.T) {
	t.Parallel()

	for _, content := range []string{
		"CREATE TABLE example (id INTEGER);",
		"-- +goose Up\n-- +goose Down\nDROP TABLE example;",
	} {
		if _, err := upMigration(content); err == nil {
			t.Fatalf("upMigration(%q) error = nil", content)
		}
	}
}

func TestMigrationVersion(t *testing.T) {
	t.Parallel()

	got, err := migrationVersion("00003_staleness.sql")
	if err != nil {
		t.Fatal(err)
	}
	if got != 3 {
		t.Fatalf("version = %d, want 3", got)
	}
}
