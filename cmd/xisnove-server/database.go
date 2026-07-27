package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/araihu/xisnove/internal/adapters/database"
	"github.com/araihu/xisnove/internal/adapters/migration"
	"github.com/araihu/xisnove/internal/adapters/postgres"
	"github.com/araihu/xisnove/internal/adapters/sqlite"
	"github.com/araihu/xisnove/internal/adapters/sqlitecompat"
	"github.com/araihu/xisnove/internal/adapters/tursocloud"
	"github.com/araihu/xisnove/internal/adapters/tursolocal"
)

func migrateCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("db migrate", flag.ContinueOnError)
	databaseFlags := addDatabaseFlags(flags)
	phase := flags.String("phase", string(migration.PhaseExpand), "migration phase: expand or contract")
	installationID := flags.String("installation-id", "default", "migration and process-lease namespace")
	lockTimeout := flags.Duration("lock-timeout", 30*time.Second, "bounded migration lock timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	config, err := databaseFlags.config()
	if err != nil {
		return err
	}
	handle, err := database.Open(ctx, config)
	if err != nil {
		return err
	}
	defer handle.Close()
	options := migration.DefaultOptions(fmt.Sprintf("xisnove-server-%d", time.Now().UnixNano()))
	options.InstallationID = *installationID
	options.LockTimeout = *lockTimeout
	if options.PollInterval > options.LockTimeout {
		options.PollInterval = options.LockTimeout
	}
	switch migration.Phase(*phase) {
	case migration.PhaseExpand:
		return migrateProfile(ctx, handle, options)
	case migration.PhaseContract:
		return checkContractProfile(ctx, handle, options, sqlitecompat.LatestMigrationVersion)
	default:
		return fmt.Errorf("invalid migration phase %q", *phase)
	}
}

func migrateProfile(ctx context.Context, handle *database.Handle, options migration.Options) error {
	switch handle.Profile {
	case database.ProfileSQLite:
		return sqlite.MigrateWithOptions(ctx, handle.DB, options)
	case database.ProfileTursoLocal:
		return tursolocal.MigrateWithOptions(ctx, handle.DB, options)
	case database.ProfileTursoCloud:
		return tursocloud.MigrateWithOptions(ctx, handle.DB, options)
	case database.ProfilePostgres:
		return postgres.MigrateWithOptions(ctx, handle.DB, options)
	default:
		return fmt.Errorf("unsupported database profile %q", handle.Profile)
	}
}

func checkContractProfile(ctx context.Context, handle *database.Handle, options migration.Options, targetSchema int64) error {
	switch handle.Profile {
	case database.ProfileSQLite:
		return sqlite.CheckContractWithOptions(ctx, handle.DB, options, targetSchema)
	case database.ProfileTursoLocal:
		return tursolocal.CheckContractWithOptions(ctx, handle.DB, options, targetSchema)
	case database.ProfileTursoCloud:
		return tursocloud.CheckContractWithOptions(ctx, handle.DB, options, targetSchema)
	case database.ProfilePostgres:
		return postgres.CheckContractWithOptions(ctx, handle.DB, options, targetSchema)
	default:
		return fmt.Errorf("unsupported database profile %q", handle.Profile)
	}
}
