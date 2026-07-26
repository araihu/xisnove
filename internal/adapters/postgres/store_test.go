package postgres

import (
	"errors"
	"fmt"
	"testing"

	"github.com/araihu/xisnove/application"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestTransactionErrorMarksWrappedPostgresSerializationFailures(t *testing.T) {
	for _, code := range []string{"40001", "40P01"} {
		t.Run(code, func(t *testing.T) {
			cause := &pgconn.PgError{Code: code, Message: "retry transaction"}
			wrapped := fmt.Errorf("database operation: %w", cause)
			err := classifyTransactionError(wrapped)
			if !errors.Is(err, application.ErrRetryableTransaction) {
				t.Fatalf("classifyTransactionError() = %v, want ErrRetryableTransaction", err)
			}
			if !errors.Is(err, cause) {
				t.Fatalf("classifyTransactionError() = %v, want original PgError", err)
			}
		})
	}
}

func TestTransactionErrorRejectsOtherPostgresCodesAndText(t *testing.T) {
	for name, cause := range map[string]error{
		"unique constraint": &pgconn.PgError{Code: "23505", Message: "duplicate"},
		"other class 40":    &pgconn.PgError{Code: "40P02", Message: "not a deadlock"},
		"serialization text": errors.New(
			"ERROR: could not serialize access (SQLSTATE 40001)",
		),
	} {
		t.Run(name, func(t *testing.T) {
			err := classifyTransactionError(cause)
			if errors.Is(err, application.ErrRetryableTransaction) {
				t.Fatalf("classifyTransactionError() = %v, unexpectedly retryable", err)
			}
		})
	}
}

func TestRepositoryErrorPreservesPostgresRetryableMarkerAndCause(t *testing.T) {
	cause := &pgconn.PgError{Code: "40001", Message: "serialization failure"}
	err := repositoryError("create location", fmt.Errorf("driver wrapper: %w", cause))
	if !errors.Is(err, application.ErrRetryableTransaction) {
		t.Fatalf("repositoryError() = %v, want ErrRetryableTransaction", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("repositoryError() = %v, want original PgError", err)
	}
}

func TestRepositoryErrorKeepsPostgresConstraintNonRetryable(t *testing.T) {
	cause := &pgconn.PgError{Code: "23505", Message: "duplicate"}
	err := repositoryError("create location", cause)
	if !errors.Is(err, application.ErrConflict) {
		t.Fatalf("repositoryError() = %v, want ErrConflict", err)
	}
	if errors.Is(err, application.ErrRetryableTransaction) {
		t.Fatalf("repositoryError() = %v, constraint unexpectedly retryable", err)
	}
}
