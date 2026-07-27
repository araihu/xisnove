package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	"github.com/araihu/xisnove/internal/adapters/database"
	"github.com/araihu/xisnove/internal/adapters/ids"
	"github.com/araihu/xisnove/internal/adapters/secrets"
)

func bootstrapCommand(ctx context.Context, args []string) error {
	flags := newCommandFlagSet("admin bootstrap")
	databaseFlags := addDatabaseFlags(flags)
	email := flags.String("email", "", "administrator email")
	passwordFile := flags.String("password-file", "", "password secret file")
	commandTimeout := flags.Duration("timeout", 2*time.Minute, "overall bootstrap command timeout")
	if err := parseCommandFlags(flags, args); err != nil {
		return err
	}
	if *email == "" || *passwordFile == "" {
		return fmt.Errorf("--email and --password-file are required")
	}
	if *commandTimeout <= 0 {
		return newCommandUsageError(fmt.Errorf("bootstrap command timeout must be positive"))
	}
	commandCtx, cancel := context.WithTimeout(ctx, *commandTimeout)
	defer cancel()
	config, err := databaseFlags.configContext(commandCtx)
	if err != nil {
		return err
	}
	password, err := readBootstrapPassword(commandCtx, *passwordFile)
	if err != nil {
		return err
	}
	handle, err := database.Open(commandCtx, config)
	if err != nil {
		return err
	}
	defer handle.Close()
	if err := handle.Ready(commandCtx); err != nil {
		return err
	}
	service := application.NewAuthService(application.AuthServiceConfig{
		Store: handle.Store, Passwords: xiscrypto.NewProductionPasswordHasher(),
		Tokens: xiscrypto.NewProductionTokenIssuer(), SessionDuration: 24 * time.Hour,
		Now: time.Now, NewID: ids.NewUUID,
	})
	return service.BootstrapAdmin(commandCtx, *email, password)
}

func readBootstrapPassword(ctx context.Context, path string) (string, error) {
	contents, err := (secrets.FileResolver{}).Resolve(ctx, port.SecretReference{
		Kind: port.SecretReferenceFile, Locator: path,
	})
	if err != nil {
		return "", fmt.Errorf("read password file: secret is unavailable or unsafe")
	}
	password := strings.TrimSpace(string(contents))
	if password == "" {
		return "", fmt.Errorf("read password file: secret is empty")
	}
	return password, nil
}
