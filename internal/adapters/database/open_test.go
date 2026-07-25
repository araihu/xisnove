package database_test

import (
	"context"
	"path/filepath"
	"testing"

	xisdatabase "github.com/araihu/xisnove/internal/adapters/database"
)

func TestOpenSQLiteHandleLifecycle(t *testing.T) {
	t.Parallel()

	handle, err := xisdatabase.Open(context.Background(), xisdatabase.Config{
		Profile: xisdatabase.ProfileSQLite,
		URL:     filepath.Join(t.TempDir(), "xisnove.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := handle.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	if handle.Profile != xisdatabase.ProfileSQLite {
		t.Fatalf("Profile = %q", handle.Profile)
	}
	if handle.ReplicaSafe {
		t.Fatal("SQLite handle unexpectedly marked replica-safe")
	}
	if handle.Store == nil {
		t.Fatal("Store is nil")
	}
	if err := handle.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := handle.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRejectsInvalidRemoteProfileWithoutLeakingSecret(t *testing.T) {
	t.Parallel()

	const secret = "do-not-leak"
	_, err := xisdatabase.Open(context.Background(), xisdatabase.Config{
		Profile:   xisdatabase.ProfileTursoCloud,
		URL:       "https://example.turso.io",
		AuthToken: secret,
	})
	if err == nil {
		t.Fatal("Open() error = nil")
	}
	if contains := stringContains(err.Error(), secret); contains {
		t.Fatalf("Open() error leaks secret: %v", err)
	}
}

func stringContains(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}
