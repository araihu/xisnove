package main

import (
	"context"
	"fmt"
	"time"

	"github.com/araihu/xisnove/internal/adapters/database"
	"github.com/araihu/xisnove/internal/adapters/migration"
	"github.com/araihu/xisnove/internal/adapters/postgres"
	"github.com/araihu/xisnove/internal/adapters/sqlite"
	"github.com/araihu/xisnove/internal/adapters/tursocloud"
	"github.com/araihu/xisnove/internal/adapters/tursolocal"
)

func migrateCommand(ctx context.Context, args []string) error {
	flags := newCommandFlagSet("db migrate")
	databaseFlags := addDatabaseFlags(flags)
	phase := flags.String("phase", string(migration.PhaseExpand), "migration phase: expand or contract")
	installationID := flags.String("installation-id", "default", "migration and process-lease namespace")
	lockTimeout := flags.Duration("lock-timeout", 30*time.Second, "bounded migration lock timeout")
	commandTimeout := flags.Duration("timeout", 2*time.Minute, "overall migration command timeout")
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	selectedPhase := migration.Phase(*phase)
	if selectedPhase != migration.PhaseExpand && selectedPhase != migration.PhaseContract {
		return newCommandUsageError(fmt.Errorf("invalid migration phase %q", *phase))
	}
	if *lockTimeout <= 0 {
		return newCommandUsageError(fmt.Errorf("migration lock timeout must be positive"))
	}
	if *commandTimeout <= 0 {
		return newCommandUsageError(fmt.Errorf("migration command timeout must be positive"))
	}
	commandCtx, cancel := context.WithTimeout(ctx, *commandTimeout)
	defer cancel()
	config, err := databaseFlags.configContext(commandCtx)
	if err != nil {
		return err
	}
	handle, err := database.Open(commandCtx, config)
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
	switch selectedPhase {
	case migration.PhaseExpand:
		return migrateProfile(commandCtx, handle, options)
	case migration.PhaseContract:
		return migrateContractProfile(commandCtx, handle, options)
	}
	panic("validated migration phase is not handled")
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

func migrateContractProfile(ctx context.Context, handle *database.Handle, options migration.Options) error {
	switch handle.Profile {
	case database.ProfileSQLite:
		return sqlite.ContractWithOptions(ctx, handle.DB, options)
	case database.ProfileTursoLocal:
		return tursolocal.ContractWithOptions(ctx, handle.DB, options)
	case database.ProfileTursoCloud:
		return tursocloud.ContractWithOptions(ctx, handle.DB, options)
	case database.ProfilePostgres:
		return postgres.ContractWithOptions(ctx, handle.DB, options)
	default:
		return fmt.Errorf("unsupported database profile %q", handle.Profile)
	}
}
