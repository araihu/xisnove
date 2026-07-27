package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/araihu/xisnove/internal/adapters/migration"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
	"github.com/araihu/xisnove/internal/buildinfo"
)

func TestExecuteVersionSkipsCommandDependencies(t *testing.T) {
	setServerBuildInfo(t, "1.2.3", "0123456789abcdef0123456789abcdef01234567", "2026-07-27T03:04:05Z", "false")
	var stdout, stderr bytes.Buffer
	called := false
	exit := execute(context.Background(), []string{"--version"}, &stdout, &stderr, func(context.Context, []string) error {
		called = true
		return nil
	})
	if exit != 0 || called || stderr.Len() != 0 {
		t.Fatalf("execute = exit %d called %t stderr %q", exit, called, stderr.String())
	}
	want := "xisnove-server version=1.2.3 commit=0123456789abcdef0123456789abcdef01234567 build_date=2026-07-27T03:04:05Z dirty=false\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestExecuteUsageErrorIsSingleDiagnosticAndExitTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := execute(context.Background(), []string{"db", "migrate", "--definitely-invalid"}, &stdout, &stderr, run)
	if exit != 2 || stdout.Len() != 0 || strings.Count(stderr.String(), "\n") != 1 {
		t.Fatalf("execute = exit %d stdout %q stderr %q, want exit 2 and one diagnostic", exit, stdout.String(), stderr.String())
	}
}

func TestMigrateInvalidPhaseDoesNotCreateDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid-phase.db")
	err := migrateCommand(context.Background(), []string{"--database", path, "--phase", "drop"})
	var usageErr *commandUsageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("migrateCommand() error = %v, want commandUsageError", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalid phase created database: stat error = %v", statErr)
	}
}

func TestExecuteMigrationFailureExitCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "contention", err: migration.NewContentionError("held"), want: 75},
		{name: "timeout", err: migration.NewTimeoutError("expired"), want: 75},
		{name: "incompatible schema", err: migration.NewIncompatibleSchemaError("too new"), want: 1},
		{name: "live incompatible process", err: migration.NewLiveIncompatibleProcessError("old process"), want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exit := execute(context.Background(), []string{"db", "migrate"}, &stdout, &stderr, func(context.Context, []string) error {
				return tt.err
			})
			if exit != tt.want || stdout.Len() != 0 || strings.Count(stderr.String(), "\n") != 1 {
				t.Fatalf("execute = exit %d stdout %q stderr %q, want exit %d", exit, stdout.String(), stderr.String(), tt.want)
			}
		})
	}
}

func TestExecuteVersionFailureIsSingleUsageDiagnostic(t *testing.T) {
	setServerBuildInfo(t, "dev", "bad", "bad", "true")
	var stdout, stderr bytes.Buffer
	exit := execute(context.Background(), []string{"--version"}, &stdout, &stderr, func(context.Context, []string) error {
		t.Fatal("command dependency initialized")
		return nil
	})
	if exit != 2 || stdout.Len() != 0 || strings.Count(stderr.String(), "\n") != 1 {
		t.Fatalf("execute = exit %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
}

func TestExecuteRejectsMalformedVersionFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := execute(context.Background(), []string{"--version", "extra"}, &stdout, &stderr, func(context.Context, []string) error {
		t.Fatal("command dependency initialized")
		return nil
	})
	if exit != 2 || stdout.Len() != 0 || strings.Count(stderr.String(), "\n") != 1 {
		t.Fatalf("execute = exit %d stdout %q stderr %q", exit, stdout.String(), stderr.String())
	}
}

func setServerBuildInfo(t *testing.T, version, commit, date, dirty string) {
	t.Helper()
	oldVersion, oldCommit, oldDate, oldDirty := buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate, buildinfo.Dirty
	buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate, buildinfo.Dirty = version, commit, date, dirty
	t.Cleanup(func() {
		buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate, buildinfo.Dirty = oldVersion, oldCommit, oldDate, oldDirty
	})
}

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
