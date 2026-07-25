package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/araihu/xisnove/application/port"
)

// ErrNotificationKeyUnavailable reports persisted configuration encrypted with
// a key version absent from the configured keyring.
var ErrNotificationKeyUnavailable = errors.New("notification key version is unavailable")

// NotificationSecretServiceConfig supplies the operational transaction
// boundary, versioned sealer, and optional clock used during key rotation.
type NotificationSecretServiceConfig struct {
	Store  port.UnitOfWork
	Sealer port.ConfigSealer
	Now    func() time.Time
}

// NotificationSecretService validates and rotates encrypted notification
// channel configuration without exposing infrastructure-specific key storage.
type NotificationSecretService struct {
	store  port.UnitOfWork
	sealer port.ConfigSealer
	now    func() time.Time
}

// NewNotificationSecretService constructs the notification secret use case.
func NewNotificationSecretService(config NotificationSecretServiceConfig) *NotificationSecretService {
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &NotificationSecretService{
		store: config.Store, sealer: config.Sealer, now: now,
	}
}

// ValidateStoredKeyVersions verifies that every persisted channel can be
// opened by the configured keyring.
func (s *NotificationSecretService) ValidateStoredKeyVersions(ctx context.Context) error {
	return s.store.View(ctx, func(ctx context.Context, repositories port.Repositories) error {
		versions, err := repositories.NotificationChannels.ListKeyVersions(ctx)
		if err != nil {
			return fmt.Errorf("list notification key versions: %w", err)
		}
		for _, version := range versions {
			if !s.sealer.CanOpen(version) {
				return fmt.Errorf("%w: %d", ErrNotificationKeyUnavailable, version)
			}
		}
		return nil
	})
}

// RotateBatch re-encrypts at most limit channels with the active key in one
// transaction. Repeating it is safe and resumes from persisted key versions.
func (s *NotificationSecretService) RotateBatch(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rotated := 0
	err := s.store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		records, err := repositories.NotificationChannels.ListNeedingKeyVersion(
			ctx, s.sealer.ActiveVersion(), limit,
		)
		if err != nil {
			return fmt.Errorf("list notification channels for rotation: %w", err)
		}
		for _, record := range records {
			identity := port.ConfigIdentity{
				ChannelID: record.Channel.ID, Kind: record.Channel.Kind,
			}
			plaintext, err := s.sealer.Open(ctx, identity, port.SealedConfig{
				KeyVersion: record.KeyVersion, Ciphertext: record.EncryptedConfig,
			})
			if err != nil {
				return fmt.Errorf("open notification channel %s for rotation: %w", record.Channel.ID, err)
			}
			sealed, sealErr := s.sealer.Seal(ctx, identity, plaintext)
			clear(plaintext)
			if sealErr != nil {
				return fmt.Errorf("seal notification channel %s for rotation: %w", record.Channel.ID, sealErr)
			}
			if sealed.KeyVersion != s.sealer.ActiveVersion() {
				return errors.New("seal notification channel for rotation: sealer returned a non-active version")
			}
			record.KeyVersion = sealed.KeyVersion
			record.EncryptedConfig = append([]byte(nil), sealed.Ciphertext...)
			record.Channel.UpdatedAt = s.now().UTC()
			updated, err := repositories.NotificationChannels.Update(ctx, record)
			if err != nil {
				return fmt.Errorf("update rotated notification channel %s: %w", record.Channel.ID, err)
			}
			if !updated {
				return fmt.Errorf("update rotated notification channel %s: %w", record.Channel.ID, ErrConflict)
			}
			rotated++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return rotated, nil
}
