package main

import (
	"context"
	"fmt"

	"github.com/araihu/xisnove/internal/adapters/backup"
	"github.com/araihu/xisnove/internal/adapters/database"
)

func backupCommand(ctx context.Context, args []string) error {
	flags := newCommandFlagSet("db backup")
	databaseFlags := addDatabaseFlags(flags)
	output := flags.String("output", "", "non-existing backup destination")
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	if *output == "" {
		return fmt.Errorf("--output is required")
	}
	config, err := databaseFlags.configContext(ctx)
	if err != nil {
		return err
	}
	if config.Profile != database.ProfileSQLite {
		return backup.Create(ctx, config.Profile, nil, *output)
	}
	handle, err := database.Open(ctx, config)
	if err != nil {
		return err
	}
	defer handle.Close()
	if err := handle.Ready(ctx); err != nil {
		return fmt.Errorf("database is not ready; run db migrate: %w", err)
	}
	return backup.Create(ctx, handle.Profile, handle.DB, *output)
}
