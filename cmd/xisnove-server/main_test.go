package main

import (
	"context"
	"path/filepath"
	"testing"

	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestMigrateCommandCreatesCurrentDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xisnove.db")
	if err := run(context.Background(), []string{
		"db", "migrate", "--database", path,
	}); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sqlitestore.Ready(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateCommandUsesProfileAwareFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xisnove.db")
	if err := run(context.Background(), []string{
		"db", "migrate",
		"--database-profile", "sqlite",
		"--database-url", path,
	}); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sqlitestore.Ready(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}

func TestBackupCommandCreatesReadySQLiteCopy(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.db")
	backupPath := filepath.Join(directory, "backup.db")
	if err := run(ctx, []string{"db", "migrate", "--database", sourcePath}); err != nil {
		t.Fatal(err)
	}
	source, err := sqlitestore.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.ExecContext(ctx, `
		INSERT INTO admins (id, email, password_hash, created_at)
		VALUES ('00000000-0000-4000-8000-000000000001', 'backup@example.com', 'hash', '2026-07-25T00:00:00Z')
	`); err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{
		"db", "backup", "--database-profile", "sqlite",
		"--database-url", sourcePath, "--output", backupPath,
	}); err != nil {
		t.Fatal(err)
	}
	restored, err := sqlitestore.Open(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := sqlitestore.Ready(ctx, restored); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := restored.QueryRowContext(ctx, "SELECT COUNT(*) FROM admins").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("restored admins = %d", count)
	}
}
