package main

import (
	"context"
	"flag"
	"fmt"

	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func migrateCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("db migrate", flag.ContinueOnError)
	database := flags.String("database", "", "SQLite database path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *database == "" {
		return fmt.Errorf("--database is required")
	}
	db, err := sqlitestore.Open(*database)
	if err != nil {
		return err
	}
	defer db.Close()
	return sqlitestore.Migrate(ctx, db)
}
