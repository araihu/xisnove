package sqlitecompat

import (
	"errors"
	"strings"
	"testing"

	"github.com/araihu/xisnove/application"
)

func TestRepositoryErrorMapsManagedLibSQLConstraint(t *testing.T) {
	t.Parallel()

	err := repositoryError(
		"open incident",
		errors.New(
			"failed to execute SQL:\n"+
				"SQLite error: UNIQUE constraint failed: incidents.monitor_id",
		),
	)
	if !errors.Is(err, application.ErrConflict) {
		t.Fatalf("repositoryError() = %v, want ErrConflict", err)
	}
}

func TestRepositoryErrorDoesNotClassifyArbitraryConstraintText(t *testing.T) {
	t.Parallel()

	err := repositoryError(
		"run query",
		errors.New("upstream constraint failed: unavailable"),
	)
	if errors.Is(err, application.ErrConflict) {
		t.Fatalf("repositoryError() = %v, unexpectedly ErrConflict", err)
	}
	if !strings.Contains(err.Error(), "upstream constraint failed") {
		t.Fatalf("repositoryError() = %v, want original cause", err)
	}
}
