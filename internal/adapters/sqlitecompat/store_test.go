package sqlitecompat

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/araihu/xisnove/application"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
	turso "turso.tech/database/tursogo"
)

func TestTransactionErrorMarksWrappedSQLiteBusy(t *testing.T) {
	cause := modernSQLiteBusyError(t)
	wantWrapped := fmt.Errorf("write location: %w", cause)

	err := classifyTransactionError(wantWrapped)

	if !errors.Is(err, application.ErrRetryableTransaction) {
		t.Fatalf("classifyTransactionError() = %v, want ErrRetryableTransaction", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("classifyTransactionError() = %v, want original SQLite cause", err)
	}
}

func TestTransactionErrorMarksWrappedTursoRetryableFailures(t *testing.T) {
	canonicalStaleSnapshot := errors.New(
		"turso: error: database snapshot is stale, rollback and retry the transaction",
	)
	for name, cause := range map[string]error{
		"busy":           turso.ErrTursoBusy,
		"stale snapshot": canonicalStaleSnapshot,
	} {
		t.Run(name, func(t *testing.T) {
			wrapped := fmt.Errorf("execute statement: %w", cause)
			err := classifyTransactionError(wrapped)
			if !errors.Is(err, application.ErrRetryableTransaction) {
				t.Fatalf("classifyTransactionError() = %v, want ErrRetryableTransaction", err)
			}
			if !errors.Is(err, cause) {
				t.Fatalf("classifyTransactionError() = %v, want original cause", err)
			}
		})
	}
}

func TestTransactionErrorRejectsSimilarTursoMessagesAndConstraints(t *testing.T) {
	for name, cause := range map[string]error{
		"prefixed stale snapshot": errors.New(
			"proxy: turso: error: database snapshot is stale, rollback and retry the transaction",
		),
		"stale snapshot suffix": errors.New(
			"turso: error: database snapshot is stale, rollback and retry the transaction later",
		),
		"constraint": turso.ErrTursoConstraint,
	} {
		t.Run(name, func(t *testing.T) {
			err := classifyTransactionError(cause)
			if errors.Is(err, application.ErrRetryableTransaction) {
				t.Fatalf("classifyTransactionError() = %v, unexpectedly retryable", err)
			}
		})
	}
}

func TestRepositoryErrorPreservesRetryableMarkerAndDriverCause(t *testing.T) {
	cause := fmt.Errorf("driver wrapper: %w", turso.ErrTursoBusy)
	err := repositoryError("create location", cause)
	if !errors.Is(err, application.ErrRetryableTransaction) {
		t.Fatalf("repositoryError() = %v, want ErrRetryableTransaction", err)
	}
	if !errors.Is(err, turso.ErrTursoBusy) {
		t.Fatalf("repositoryError() = %v, want ErrTursoBusy", err)
	}
}

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
	if errors.Is(err, application.ErrRetryableTransaction) {
		t.Fatalf("repositoryError() = %v, constraint unexpectedly retryable", err)
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

func modernSQLiteBusyError(t *testing.T) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "busy.db")
	first, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if _, err := first.Exec(`CREATE TABLE values_for_lock (value TEXT UNIQUE)`); err != nil {
		t.Fatal(err)
	}
	tx, err := first.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if _, err := tx.Exec(`INSERT INTO values_for_lock (value) VALUES ('first')`); err != nil {
		t.Fatal(err)
	}
	_, err = second.Exec(`INSERT INTO values_for_lock (value) VALUES ('second')`)
	if err == nil {
		t.Fatal("second writer unexpectedly acquired the SQLite lock")
	}
	var sqliteErr *modernsqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code()&0xff != sqlite3.SQLITE_BUSY {
		t.Fatalf("second writer error = %T %v, want SQLITE_BUSY", err, err)
	}
	return err
}
