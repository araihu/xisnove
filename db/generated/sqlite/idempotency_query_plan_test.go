package dbsqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	migrations "github.com/araihu/xisnove/db/migrations/sqlite"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

func TestExpiredIdempotencyCleanupUsesExpiryIndex(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "idempotency-plan.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}

	rows, err := db.QueryContext(
		ctx,
		"EXPLAIN QUERY PLAN\n"+deleteExpiredIdempotencyRecords,
		"2026-07-26T12:00:00Z",
		100,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	plan := strings.Join(details, "\n")
	usesExpirySearch := strings.Contains(plan, "SEARCH idempotency_records USING INDEX idempotency_records_expiry") ||
		strings.Contains(plan, "SEARCH idempotency_records USING COVERING INDEX idempotency_records_expiry")
	if !usesExpirySearch {
		t.Fatalf("cleanup query does not search the expiry index:\n%s", plan)
	}
	if strings.Contains(plan, "SCAN idempotency_records") {
		t.Fatalf("cleanup query performs a full index or table scan:\n%s", plan)
	}
	if strings.Contains(plan, "USE TEMP B-TREE") {
		t.Fatalf("cleanup query sorts through a temporary B-tree:\n%s", plan)
	}
}
