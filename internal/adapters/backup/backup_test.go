package backup_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/araihu/xisnove/internal/adapters/backup"
	"github.com/araihu/xisnove/internal/adapters/database"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestCreateSQLiteOnlineBackup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sourcePath := filepath.Join(t.TempDir(), "source.db")
	destination := filepath.Join(t.TempDir(), "backup.db")
	source, err := sqlitestore.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	if _, err := source.ExecContext(ctx, "CREATE TABLE values_to_keep (value TEXT NOT NULL); INSERT INTO values_to_keep VALUES ('durable')"); err != nil {
		t.Fatal(err)
	}

	if err := backup.Create(ctx, database.ProfileSQLite, source, destination); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("backup permissions = %o, want owner-only", info.Mode().Perm())
	}

	restored, err := sqlitestore.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	var value string
	if err := restored.QueryRowContext(ctx, "SELECT value FROM values_to_keep").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "durable" {
		t.Fatalf("restored value = %q", value)
	}
}

func TestCreateRefusesExistingDestination(t *testing.T) {
	t.Parallel()

	source, err := sqlitestore.Open(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	destination := filepath.Join(t.TempDir(), "existing.db")
	if err := os.WriteFile(destination, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = backup.Create(context.Background(), database.ProfileSQLite, source, destination)
	if !errors.Is(err, backup.ErrDestinationExists) {
		t.Fatalf("Create() error = %v, want ErrDestinationExists", err)
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "preserve" {
		t.Fatalf("destination content = %q", content)
	}
}

func TestCreateFailsClosedForNonSQLiteProfiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		profile database.Profile
		want    error
	}{
		{database.ProfileTursoLocal, backup.ErrQuiescedBackupRequired},
		{database.ProfileTursoCloud, backup.ErrProviderBackupRequired},
		{database.ProfilePostgres, backup.ErrExternalToolRequired},
	}
	for _, test := range tests {
		err := backup.Create(context.Background(), test.profile, nil, "unused")
		if !errors.Is(err, test.want) {
			t.Fatalf("Create(%s) error = %v, want %v", test.profile, err, test.want)
		}
	}
}
