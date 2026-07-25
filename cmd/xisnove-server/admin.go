package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	"github.com/araihu/xisnove/internal/adapters/ids"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
	"github.com/araihu/xisnove/internal/application"
)

func bootstrapCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("admin bootstrap", flag.ContinueOnError)
	database := flags.String("database", "", "SQLite database path")
	email := flags.String("email", "", "administrator email")
	passwordFile := flags.String("password-file", "", "password secret file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *database == "" || *email == "" || *passwordFile == "" {
		return fmt.Errorf("--database, --email, and --password-file are required")
	}
	password, err := os.ReadFile(*passwordFile)
	if err != nil {
		return fmt.Errorf("read password file: %w", err)
	}
	db, err := sqlitestore.Open(*database)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := sqlitestore.Ready(ctx, db); err != nil {
		return err
	}
	store := sqlitestore.NewStore(db)
	service := application.NewAuthService(application.AuthServiceConfig{
		Store: store, Passwords: xiscrypto.NewProductionPasswordHasher(),
		Tokens: xiscrypto.NewProductionTokenIssuer(), SessionDuration: 24 * time.Hour,
		Now: time.Now, NewID: ids.NewUUID,
	})
	return service.BootstrapAdmin(ctx, *email, strings.TrimSpace(string(password)))
}
