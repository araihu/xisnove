package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestNotificationKeyFlagsUseEnvironmentAndAllowFlagOverride(t *testing.T) {
	t.Setenv(notificationMasterKeyFileEnvironment, "/environment/keyring.json")
	flags := newTestFlagSet(t)
	values := addNotificationKeyFlags(flags, os.Getenv)
	if err := flags.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if values.masterKeyFile != "/environment/keyring.json" {
		t.Fatalf("master key file = %q", values.masterKeyFile)
	}

	flags = newTestFlagSet(t)
	values = addNotificationKeyFlags(flags, os.Getenv)
	if err := flags.Parse([]string{"--notification-master-key-file", "/flag/keyring.json"}); err != nil {
		t.Fatal(err)
	}
	if values.masterKeyFile != "/flag/keyring.json" {
		t.Fatalf("overridden master key file = %q", values.masterKeyFile)
	}
}

func TestValidateNotificationKeyringRequiresKeysOnlyForConfiguredChannels(t *testing.T) {
	ctx := context.Background()
	store, closeStore := migratedSQLiteStore(t, ctx)
	defer closeStore()
	if err := validateNotificationKeyring(ctx, store, nil); err != nil {
		t.Fatalf("empty store validation = %v", err)
	}
	channel := newTestChannel(t)
	if err := store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		return repositories.NotificationChannels.Create(ctx, port.NotificationChannelRecord{
			Channel: channel, EncryptedConfig: []byte("ciphertext"), KeyVersion: 7,
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := validateNotificationKeyring(ctx, store, nil); err == nil || strings.Contains(err.Error(), "ciphertext") {
		t.Fatalf("validation error = %v", err)
	}
	sealer, err := xiscrypto.NewEnvelope(8, map[uint32][]byte{
		8: bytes.Repeat([]byte{8}, 32),
	}, bytes.NewReader(bytes.Repeat([]byte{1}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateNotificationKeyring(ctx, store, sealer); err == nil || !strings.Contains(err.Error(), "7") {
		t.Fatalf("missing version error = %v", err)
	}
}

func TestRotateNotificationKeysCommandReencryptsAndIsRestartSafe(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "xisnove.db")
	if err := run(ctx, []string{"db", "migrate", "--database", databasePath}); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(db)
	channel := newTestChannel(t)
	key1 := bytes.Repeat([]byte{1}, 32)
	key2 := bytes.Repeat([]byte{2}, 32)
	oldEnvelope, err := xiscrypto.NewEnvelope(1, map[uint32][]byte{1: key1}, bytes.NewReader(bytes.Repeat([]byte{3}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := oldEnvelope.Seal(ctx, port.ConfigIdentity{ChannelID: channel.ID, Kind: channel.Kind}, []byte("service://credential"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		return repositories.NotificationChannels.Create(ctx, port.NotificationChannelRecord{
			Channel: channel, EncryptedConfig: sealed.Ciphertext, KeyVersion: sealed.KeyVersion,
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	keyringPath := filepath.Join(directory, "keyring.json")
	writeKeyring(t, keyringPath, 2, map[uint32][]byte{1: key1, 2: key2})
	args := []string{
		"notifications", "keys", "rotate", "--database", databasePath,
		"--notification-master-key-file", keyringPath, "--batch-size", "1",
	}
	if err := run(ctx, args); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, args); err != nil {
		t.Fatalf("restart-safe rerun = %v", err)
	}

	db, err = sqlitestore.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	record, err := sqlitestore.NewStore(db).Repositories().NotificationChannels.Get(ctx, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.KeyVersion != 2 {
		t.Fatalf("key version = %d", record.KeyVersion)
	}
	currentEnvelope, err := xiscrypto.LoadEnvelope(keyringPath)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := currentEnvelope.Open(ctx, port.ConfigIdentity{ChannelID: channel.ID, Kind: channel.Kind}, port.SealedConfig{
		KeyVersion: record.KeyVersion, Ciphertext: record.EncryptedConfig,
	})
	if err != nil || string(plaintext) != "service://credential" {
		t.Fatalf("rotated plaintext = %q, %v", plaintext, err)
	}
}

func TestRotateNotificationKeysCommandValidatesInputsBeforeDatabaseAccess(t *testing.T) {
	if err := run(context.Background(), []string{"notifications", "keys", "rotate", "--batch-size", "0"}); err == nil {
		t.Fatal("zero batch size accepted")
	}
	if err := run(context.Background(), []string{"notifications", "keys", "rotate", "--database", "missing.db"}); err == nil {
		t.Fatal("missing keyring accepted")
	}
}

func newTestFlagSet(t *testing.T) *flag.FlagSet {
	t.Helper()
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.SetOutput(&bytes.Buffer{})
	return flags
}

func migratedSQLiteStore(t *testing.T, ctx context.Context) (port.Store, func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "xisnove.db")
	db, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	return sqlitestore.NewStore(db), func() { _ = db.Close() }
}

func newTestChannel(t *testing.T) domain.NotificationChannel {
	t.Helper()
	channel, err := domain.NewNotificationChannel(
		"00000000-0000-4000-8000-000000000001",
		"primary",
		domain.NotificationChannelShoutrrr,
		true,
		time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	return channel
}

func writeKeyring(t *testing.T, path string, active uint32, keys map[uint32][]byte) {
	t.Helper()
	contents := fmt.Sprintf(
		`{"activeVersion":%d,"keys":[{"version":1,"key":%q},{"version":2,"key":%q}]}`,
		active,
		base64.StdEncoding.EncodeToString(keys[1]),
		base64.StdEncoding.EncodeToString(keys[2]),
	)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
