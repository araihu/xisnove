package main

import (
	"context"
	"flag"

	"github.com/araihu/xisnove/internal/adapters/database"
)

func migrateCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("db migrate", flag.ContinueOnError)
	databaseFlags := addDatabaseFlags(flags)
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
	return handle.Migrate(ctx)
}
