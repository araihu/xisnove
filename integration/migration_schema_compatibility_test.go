package integration_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/araihu/xisnove/internal/adapters/database"
	migrationcontract "github.com/araihu/xisnove/internal/adapters/migration"
	"github.com/araihu/xisnove/internal/adapters/sqlite"
	"github.com/araihu/xisnove/internal/adapters/tursolocal"
)

func TestMigrationSchemaIntervalAndContractFenceAcrossLocalProfiles(t *testing.T) {
	for _, profile := range []database.Profile{database.ProfileSQLite, database.ProfileTursoLocal} {
		profile := profile
		t.Run(string(profile), func(t *testing.T) {
			ctx := context.Background()
			handle, err := database.Open(ctx, database.Config{Profile: profile, URL: filepath.Join(t.TempDir(), "schema.db")})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = handle.Close() })
			if err := handle.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := handle.DB.ExecContext(ctx, `UPDATE schema_migrations SET is_applied = 0 WHERE version_id = ?`, sqlite.LatestMigrationVersion); err != nil {
				t.Fatal(err)
			}
			if err := handle.Ready(ctx); err != nil {
				t.Fatalf("minimum supported schema rejected after expand migration: %v", err)
			}
			if _, err := handle.DB.ExecContext(ctx, `UPDATE schema_migrations SET is_applied = 1 WHERE version_id = ?`, sqlite.LatestMigrationVersion); err != nil {
				t.Fatal(err)
			}
			lease := migrationcontract.ProcessLease{InstallationID: "shared", ProcessID: "pre-M6", ProcessVersion: "pre-M6", Readable: migrationcontract.SchemaInterval{Minimum: sqlite.MinimumMigrationVersion, Maximum: sqlite.MinimumMigrationVersion}, TTL: time.Minute}
			var acquire func(context.Context, migrationcontract.ProcessLease) error
			var release func(context.Context, string, string) error
			var check func(context.Context, string, int64) error
			switch profile {
			case database.ProfileSQLite:
				acquire = func(ctx context.Context, lease migrationcontract.ProcessLease) error {
					return sqlite.AcquireProcessLease(ctx, handle.DB, lease)
				}
				release = func(ctx context.Context, installationID, processID string) error {
					return sqlite.ReleaseProcessLease(ctx, handle.DB, installationID, processID)
				}
				check = func(ctx context.Context, installationID string, target int64) error {
					return sqlite.CheckContractAllowed(ctx, handle.DB, installationID, target)
				}
			case database.ProfileTursoLocal:
				acquire = func(ctx context.Context, lease migrationcontract.ProcessLease) error {
					return tursolocal.AcquireProcessLease(ctx, handle.DB, lease)
				}
				release = func(ctx context.Context, installationID, processID string) error {
					return tursolocal.ReleaseProcessLease(ctx, handle.DB, installationID, processID)
				}
				check = func(ctx context.Context, installationID string, target int64) error {
					return tursolocal.CheckContractAllowed(ctx, handle.DB, installationID, target)
				}
			}
			if err := acquire(ctx, lease); err != nil {
				t.Fatal(err)
			}
			if err := check(ctx, "shared", sqlite.LatestMigrationVersion); !errors.Is(err, migrationcontract.ErrLiveIncompatibleProcess) {
				t.Fatalf("contract fence error = %v", err)
			}
			if err := release(ctx, "shared", "pre-M6"); err != nil {
				t.Fatal(err)
			}
			if err := check(ctx, "shared", sqlite.LatestMigrationVersion); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigrationConcurrentLocalProfilesConverge(t *testing.T) {
	for _, profile := range []database.Profile{database.ProfileSQLite, database.ProfileTursoLocal} {
		profile := profile
		t.Run(string(profile), func(t *testing.T) {
			ctx := context.Background()
			config := database.Config{Profile: profile, URL: filepath.Join(t.TempDir(), "concurrent.db")}
			first, err := database.Open(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = first.Close() })
			second, err := database.Open(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = second.Close() })
			start := make(chan struct{})
			failures := make(chan error, 2)
			var wg sync.WaitGroup
			for index, handle := range []*database.Handle{first, second} {
				wg.Add(1)
				go func(index int, handle *database.Handle) {
					defer wg.Done()
					<-start
					options := migrationcontract.DefaultOptions(fmt.Sprintf("%s-%d", profile, index))
					switch profile {
					case database.ProfileSQLite:
						failures <- sqlite.MigrateWithOptions(ctx, handle.DB, options)
					case database.ProfileTursoLocal:
						failures <- tursolocal.MigrateWithOptions(ctx, handle.DB, options)
					}
				}(index, handle)
			}
			close(start)
			wg.Wait()
			close(failures)
			for err := range failures {
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := first.Ready(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
