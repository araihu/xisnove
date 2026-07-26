package postgres

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	application "github.com/araihu/xisnove/application/port"
	dbpostgres "github.com/araihu/xisnove/db/generated/postgres"
	postgrescontainer "github.com/araihu/xisnove/internal/testsupport/postgrescontainer"
	"github.com/google/uuid"
)

func TestAPITokenMappingRejectsMalformedStoredScopes(t *testing.T) {
	_, err := mapPostgresAPIToken(dbpostgres.ApiToken{
		ID: "00000000-0000-4000-8000-000000000001", AdminID: "00000000-0000-4000-8000-000000000002",
		Label: "corrupt", TokenHash: []byte{1}, ScopesJson: []byte(`{`), CreatedAt: time.Now(),
	})
	if err == nil {
		t.Fatal("mapPostgresAPIToken() accepted malformed scopes JSON")
	}
}

func TestAPITokenMappingRejectsInvalidStoredScopes(t *testing.T) {
	ctx := context.Background()
	db := openPostgresHumanClientTestDatabase(t, ctx)
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	if _, err := db.ExecContext(ctx, `INSERT INTO admins (id,email,password_hash,created_at) VALUES ($1,$2,$3,$4)`,
		"00000000-0000-4000-8000-000000000001", "admin@example.com", "hash", now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO api_tokens (id,admin_id,label,token_hash,scopes_json,created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		"00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000001", "corrupt", []byte{1}, `["tokens:read"]`, now); err != nil {
		t.Fatal(err)
	}
	repository := newRepositories(dbpostgres.New(db)).APITokens
	for name, raw := range map[string]string{
		"empty":     `[]`,
		"unknown":   `["root:all"]`,
		"duplicate": `["tokens:read","tokens:read"]`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := db.ExecContext(ctx, `UPDATE api_tokens SET scopes_json = $1 WHERE id = $2`, raw, "00000000-0000-4000-8000-000000000002"); err != nil {
				t.Fatal(err)
			}
			if _, err := repository.List(ctx, application.PageRequest{Limit: 1}); err == nil {
				t.Fatal("List() accepted invalid stored scopes")
			}
		})
	}
}

func openPostgresHumanClientTestDatabase(t *testing.T, ctx context.Context) *sql.DB {
	t.Helper()
	baseURL := postgrescontainer.URL(t, os.Getenv("XISNOVE_TEST_POSTGRES_URL"))
	admin, err := Open(ctx, baseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "xisnove_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	databaseURL, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := databaseURL.Query()
	query.Set("search_path", schema)
	databaseURL.RawQuery = query.Encode()
	db, err := Open(ctx, databaseURL.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}
