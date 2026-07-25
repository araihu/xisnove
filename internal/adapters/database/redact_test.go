package database

import (
	"errors"
	"strings"
	"testing"
)

func TestRedactDatabaseErrorRemovesURLCredentialsAndToken(t *testing.T) {
	t.Parallel()

	cause := errors.New("connect postgres://alice:db-secret@example.test/db?api_token=query-secret with bearer-secret")
	err := redactDatabaseError(cause, Config{
		Profile:   ProfilePostgres,
		URL:       "postgres://alice:db-secret@example.test/db?api_token=query-secret",
		AuthToken: "bearer-secret",
	})
	for _, secret := range []string{"alice", "db-secret", "query-secret", "bearer-secret", "postgres://"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("redacted error leaked %q: %v", secret, err)
		}
	}
	if !errors.Is(err, cause) {
		t.Fatal("redacted error did not preserve its cause")
	}
}
