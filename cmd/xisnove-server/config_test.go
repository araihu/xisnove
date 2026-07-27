package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/araihu/xisnove/internal/adapters/database"
)

func TestDatabaseFlagsParseEveryProfile(t *testing.T) {
	t.Parallel()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("  secret-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		args      []string
		profile   database.Profile
		url       string
		authToken string
	}{
		{"sqlite", []string{"--database-profile", "sqlite", "--database-url", "local.db"}, database.ProfileSQLite, "local.db", ""},
		{"local Turso", []string{"--database-profile", "turso-local", "--database-url", "local.turso"}, database.ProfileTursoLocal, "local.turso", ""},
		{"managed Turso", []string{"--database-profile", "turso-cloud", "--database-url", "libsql://example.turso.io", "--database-auth-token-file", tokenFile}, database.ProfileTursoCloud, "libsql://example.turso.io", "secret-token"},
		{"PostgreSQL", []string{"--database-profile", "postgres", "--database-url", "postgres://user:secret@example/db"}, database.ProfilePostgres, "postgres://user:secret@example/db", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			flags := flag.NewFlagSet("test", flag.ContinueOnError)
			values := addDatabaseFlags(flags)
			if err := flags.Parse(test.args); err != nil {
				t.Fatal(err)
			}
			got, err := values.config()
			if err != nil {
				t.Fatal(err)
			}
			if got.Profile != test.profile || got.URL != test.url || got.AuthToken != test.authToken {
				t.Fatalf("config = %+v", got)
			}
			if strings.Contains(got.String(), "secret") || strings.Contains(got.String(), test.url) {
				t.Fatalf("Config.String() leaked credentials or URL: %s", got)
			}
		})
	}
}

func TestDatabaseFlagsKeepDeprecatedSQLiteAlias(t *testing.T) {
	t.Parallel()
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	values := addDatabaseFlags(flags)
	if err := flags.Parse([]string{"--database", "legacy.db"}); err != nil {
		t.Fatal(err)
	}
	got, err := values.config()
	if err != nil {
		t.Fatal(err)
	}
	if got.Profile != database.ProfileSQLite || got.URL != "legacy.db" {
		t.Fatalf("config = %+v", got)
	}
}

func TestDatabaseFlagsLoadProjectedDatabaseURLFile(t *testing.T) {
	t.Parallel()
	target := filepath.Join(t.TempDir(), "database-url")
	if err := os.WriteFile(target, []byte("postgres://user:secret@example.test/xisnove\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "projected-database-url")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	values := addDatabaseFlags(flags)
	if err := flags.Parse([]string{"--database-profile", "postgres", "--database-url-file", link}); err != nil {
		t.Fatal(err)
	}
	got, err := values.config()
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "postgres://user:secret@example.test/xisnove" {
		t.Fatalf("database URL = %q", got.URL)
	}
}

func TestDatabaseFlagsRejectAmbiguousOrUnsafeInputs(t *testing.T) {
	t.Parallel()
	tests := [][]string{
		{"--database", "legacy.db", "--database-url", "new.db"},
		{"--database-url", "new.db", "--database-url-file", "database-url"},
		{"--database", "legacy.db", "--database-url-file", "database-url"},
		{"--database-profile", "postgres", "--database", "legacy.db"},
		{"--database-profile", "sqlite", "--database-url", "local.db", "--database-auth-token-file", "token"},
		{"--database-profile", "unknown", "--database-url", "value"},
	}
	for _, args := range tests {
		flags := flag.NewFlagSet("test", flag.ContinueOnError)
		values := addDatabaseFlags(flags)
		if err := flags.Parse(args); err != nil {
			t.Fatal(err)
		}
		if _, err := values.config(); err == nil {
			t.Fatalf("config(%v) error = nil", args)
		}
	}
}

func TestDatabaseAuthTokenFileReadErrorIsStable(t *testing.T) {
	t.Parallel()
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	values := addDatabaseFlags(flags)
	missing := filepath.Join(t.TempDir(), "missing")
	if err := flags.Parse([]string{
		"--database-profile", "turso-cloud", "--database-url", "libsql://example.turso.io",
		"--database-auth-token-file", missing,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := values.config()
	if err == nil || !strings.HasPrefix(err.Error(), "read database auth token file:") {
		t.Fatalf("config() error = %v", err)
	}
}

func TestValidateReplicaCount(t *testing.T) {
	t.Parallel()
	for _, profile := range []database.Profile{database.ProfileSQLite, database.ProfileTursoLocal} {
		if err := validateReplicaCount(profile, 2); err == nil {
			t.Fatalf("validateReplicaCount(%s, 2) error = nil", profile)
		}
	}
	for _, profile := range []database.Profile{database.ProfilePostgres, database.ProfileTursoCloud} {
		if err := validateReplicaCount(profile, 2); err != nil {
			t.Fatalf("validateReplicaCount(%s, 2) error = %v", profile, err)
		}
	}
}
