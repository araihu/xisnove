package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	"github.com/araihu/xisnove/internal/adapters/database"
)

const notificationMasterKeyFileEnvironment = "XISNOVE_NOTIFICATION_MASTER_KEY_FILE"

type notificationKeyFlagValues struct {
	masterKeyFile string
}

func addNotificationKeyFlags(
	flags *flag.FlagSet,
	getenv func(string) string,
) *notificationKeyFlagValues {
	values := &notificationKeyFlagValues{}
	flags.StringVar(
		&values.masterKeyFile,
		"notification-master-key-file",
		strings.TrimSpace(getenv(notificationMasterKeyFileEnvironment)),
		"notification master-key keyring file",
	)
	return values
}

func (v *notificationKeyFlagValues) load() (port.ConfigSealer, error) {
	if strings.TrimSpace(v.masterKeyFile) == "" {
		return nil, nil
	}
	sealer, err := xiscrypto.LoadEnvelope(v.masterKeyFile)
	if err != nil {
		return nil, err
	}
	return sealer, nil
}

func validateNotificationKeyring(
	ctx context.Context,
	store port.UnitOfWork,
	sealer port.ConfigSealer,
) error {
	if sealer != nil {
		service := application.NewNotificationSecretService(application.NotificationSecretServiceConfig{
			Store: store, Sealer: sealer,
		})
		return service.ValidateStoredKeyVersions(ctx)
	}
	return store.View(ctx, func(ctx context.Context, repositories port.Repositories) error {
		versions, err := repositories.NotificationChannels.ListKeyVersions(ctx)
		if err != nil {
			return fmt.Errorf("list notification key versions: %w", err)
		}
		if len(versions) != 0 {
			return errors.New("notification master-key keyring is required when channels are configured")
		}
		return nil
	})
}

func logNotificationKeyring(sealer port.ConfigSealer) {
	if sealer == nil {
		slog.Info("notification keyring configuration", "configured", false)
		return
	}
	slog.Info(
		"notification keyring configuration",
		"configured", true,
		"active_version", sealer.ActiveVersion(),
	)
}

func rotateNotificationKeysCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("notifications keys rotate", flag.ContinueOnError)
	databaseFlags := addDatabaseFlags(flags)
	keyFlags := addNotificationKeyFlags(flags, os.Getenv)
	batchSize := flags.Int("batch-size", 100, "maximum channels re-encrypted per transaction")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *batchSize <= 0 || *batchSize > 1000 {
		return errors.New("--batch-size must be between 1 and 1000")
	}
	sealer, err := keyFlags.load()
	if err != nil {
		return err
	}
	if sealer == nil {
		return errors.New("--notification-master-key-file or XISNOVE_NOTIFICATION_MASTER_KEY_FILE is required")
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
	if err := handle.Ready(ctx); err != nil {
		return fmt.Errorf("database is not ready; run db migrate: %w", err)
	}
	if err := validateNotificationKeyring(ctx, handle.Store, sealer); err != nil {
		return err
	}
	service := application.NewNotificationSecretService(application.NotificationSecretServiceConfig{
		Store: handle.Store, Sealer: sealer,
	})
	total := 0
	for {
		rotated, err := service.RotateBatch(ctx, *batchSize)
		if err != nil {
			return err
		}
		total += rotated
		if rotated < *batchSize {
			break
		}
	}
	slog.Info("notification key rotation complete", "rotated_channels", total, "active_version", sealer.ActiveVersion())
	return nil
}
