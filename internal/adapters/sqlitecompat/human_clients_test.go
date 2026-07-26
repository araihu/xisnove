package sqlitecompat

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	application "github.com/araihu/xisnove/application/port"
	dbsqlite "github.com/araihu/xisnove/db/generated/sqlite"
)

func TestAPITokenMappingRejectsCorruptStoredScopes(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "corrupt-scopes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA ignore_check_constraints = ON"); err != nil {
		t.Fatal(err)
	}
	now := formatTime(time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC))
	if _, err := db.ExecContext(ctx, `INSERT INTO admins (id,email,password_hash,created_at) VALUES ('admin','admin@example.com','hash',?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO api_tokens (id,admin_id,label,token_hash,scopes_json,created_at) VALUES ('token','admin','corrupt',X'01','["tokens:read"]',?)`, now); err != nil {
		t.Fatal(err)
	}
	repository := newRepositories(dbsqlite.New(db)).APITokens
	for name, raw := range map[string]string{
		"malformed": `{`,
		"empty":     `[]`,
		"unknown":   `["root:all"]`,
		"duplicate": `["tokens:read","tokens:read"]`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := db.ExecContext(ctx, `UPDATE api_tokens SET scopes_json = ? WHERE id = 'token'`, raw); err != nil {
				t.Fatal(err)
			}
			if _, err := repository.List(ctx, application.PageRequest{Limit: 1}); err == nil {
				t.Fatal("List() accepted corrupt stored scopes")
			}
		})
	}
}
