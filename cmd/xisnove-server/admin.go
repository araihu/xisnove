package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	"github.com/araihu/xisnove/internal/adapters/database"
	"github.com/araihu/xisnove/internal/adapters/ids"
	"github.com/araihu/xisnove/internal/application"
)

func bootstrapCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("admin bootstrap", flag.ContinueOnError)
	databaseFlags := addDatabaseFlags(flags)
	email := flags.String("email", "", "administrator email")
	passwordFile := flags.String("password-file", "", "password secret file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *email == "" || *passwordFile == "" {
		return fmt.Errorf("--email and --password-file are required")
	}
	config, err := databaseFlags.config()
	if err != nil {
		return err
	}
	password, err := os.ReadFile(*passwordFile)
	if err != nil {
		return fmt.Errorf("read password file: %w", err)
	}
	handle, err := database.Open(ctx, config)
	if err != nil {
		return err
	}
	defer handle.Close()
	if err := handle.Ready(ctx); err != nil {
		return err
	}
	service := application.NewAuthService(application.AuthServiceConfig{
		Store: handle.Store, Passwords: xiscrypto.NewProductionPasswordHasher(),
		Tokens: xiscrypto.NewProductionTokenIssuer(), SessionDuration: 24 * time.Hour,
		Now: time.Now, NewID: ids.NewUUID,
	})
	return service.BootstrapAdmin(ctx, *email, strings.TrimSpace(string(password)))
}
