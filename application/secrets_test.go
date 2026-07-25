package application_test

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

func TestNotificationSecretServiceValidatesAndRotatesRestartSafeBatches(t *testing.T) {
	now := time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC)
	repository := &channelRepository{records: map[domain.NotificationChannelID]port.NotificationChannelRecord{}}
	for index, version := range []uint32{1, 1, 2} {
		id := domain.NotificationChannelID(string(rune('a' + index)))
		channel, err := domain.NewNotificationChannel(id, string(id), domain.NotificationChannelShoutrrr, true, now)
		if err != nil {
			t.Fatal(err)
		}
		repository.records[id] = port.NotificationChannelRecord{
			Channel: channel, EncryptedConfig: []byte{byte(version), byte(index)}, KeyVersion: version,
		}
	}
	store := &channelUnitOfWork{repository: repository}
	service := application.NewNotificationSecretService(application.NotificationSecretServiceConfig{
		Store: store, Sealer: &versionSealer{active: 2, versions: map[uint32]bool{1: true, 2: true}},
		Now: func() time.Time { return now.Add(time.Hour) },
	})
	if err := service.ValidateStoredKeyVersions(context.Background()); err != nil {
		t.Fatal(err)
	}
	rotated, err := service.RotateBatch(context.Background(), 1)
	if err != nil || rotated != 1 {
		t.Fatalf("first RotateBatch() = %d, %v", rotated, err)
	}
	rotated, err = service.RotateBatch(context.Background(), 1)
	if err != nil || rotated != 1 {
		t.Fatalf("second RotateBatch() = %d, %v", rotated, err)
	}
	rotated, err = service.RotateBatch(context.Background(), 1)
	if err != nil || rotated != 0 {
		t.Fatalf("completed RotateBatch() = %d, %v", rotated, err)
	}
	for _, record := range repository.records {
		if record.KeyVersion != 2 || record.Channel.UpdatedAt != now.Add(time.Hour) && record.Channel.ID != "c" {
			t.Fatalf("rotated record = %#v", record)
		}
	}
}

func TestNotificationSecretServiceRollsBackBatchAndRejectsUnknownVersion(t *testing.T) {
	now := time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC)
	channel, err := domain.NewNotificationChannel("channel", "channel", domain.NotificationChannelShoutrrr, true, now)
	if err != nil {
		t.Fatal(err)
	}
	repository := &channelRepository{records: map[domain.NotificationChannelID]port.NotificationChannelRecord{
		channel.ID: {Channel: channel, EncryptedConfig: []byte{1}, KeyVersion: 1},
	}}
	store := &channelUnitOfWork{repository: repository}
	sealer := &versionSealer{active: 2, versions: map[uint32]bool{2: true}, openErr: errors.New("old key missing")}
	service := application.NewNotificationSecretService(application.NotificationSecretServiceConfig{
		Store: store, Sealer: sealer, Now: func() time.Time { return now },
	})
	if err := service.ValidateStoredKeyVersions(context.Background()); !errors.Is(err, application.ErrNotificationKeyUnavailable) {
		t.Fatalf("ValidateStoredKeyVersions() = %v", err)
	}
	if rotated, err := service.RotateBatch(context.Background(), 10); err == nil || rotated != 0 {
		t.Fatalf("RotateBatch() = %d, %v", rotated, err)
	}
	if repository.records[channel.ID].KeyVersion != 1 {
		t.Fatal("failed rotation committed")
	}
}

type versionSealer struct {
	active   uint32
	versions map[uint32]bool
	openErr  error
}

func (s *versionSealer) ActiveVersion() uint32       { return s.active }
func (s *versionSealer) CanOpen(version uint32) bool { return s.versions[version] }
func (s *versionSealer) Open(_ context.Context, _ port.ConfigIdentity, sealed port.SealedConfig) ([]byte, error) {
	if s.openErr != nil {
		return nil, s.openErr
	}
	return append([]byte(nil), sealed.Ciphertext...), nil
}
func (s *versionSealer) Seal(_ context.Context, _ port.ConfigIdentity, plaintext []byte) (port.SealedConfig, error) {
	return port.SealedConfig{KeyVersion: s.active, Ciphertext: append([]byte{byte(s.active)}, plaintext...)}, nil
}

type channelRepository struct {
	records map[domain.NotificationChannelID]port.NotificationChannelRecord
}

func (r *channelRepository) Create(_ context.Context, record port.NotificationChannelRecord) error {
	r.records[record.Channel.ID] = cloneChannelRecord(record)
	return nil
}
func (r *channelRepository) Get(_ context.Context, id domain.NotificationChannelID) (port.NotificationChannelRecord, error) {
	record, exists := r.records[id]
	if !exists {
		return port.NotificationChannelRecord{}, port.ErrNotFound
	}
	return cloneChannelRecord(record), nil
}
func (r *channelRepository) List(context.Context, int, int) ([]port.NotificationChannelRecord, error) {
	return nil, nil
}
func (r *channelRepository) Update(_ context.Context, record port.NotificationChannelRecord) (bool, error) {
	if _, exists := r.records[record.Channel.ID]; !exists {
		return false, nil
	}
	r.records[record.Channel.ID] = cloneChannelRecord(record)
	return true, nil
}
func (r *channelRepository) SetEnabled(context.Context, domain.NotificationChannelID, bool, time.Time) (bool, error) {
	return false, nil
}
func (r *channelRepository) ListKeyVersions(context.Context) ([]uint32, error) {
	seen := map[uint32]bool{}
	for _, record := range r.records {
		seen[record.KeyVersion] = true
	}
	versions := make([]uint32, 0, len(seen))
	for version := range seen {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	return versions, nil
}
func (r *channelRepository) ListNeedingKeyVersion(_ context.Context, active uint32, limit int) ([]port.NotificationChannelRecord, error) {
	ids := make([]string, 0, len(r.records))
	for id, record := range r.records {
		if record.KeyVersion != active {
			ids = append(ids, string(id))
		}
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	result := make([]port.NotificationChannelRecord, 0, len(ids))
	for _, id := range ids {
		result = append(result, cloneChannelRecord(r.records[domain.NotificationChannelID(id)]))
	}
	return result, nil
}

type channelUnitOfWork struct{ repository *channelRepository }

func (u *channelUnitOfWork) View(ctx context.Context, fn func(context.Context, port.Repositories) error) error {
	return fn(ctx, port.Repositories{NotificationChannels: u.repository})
}
func (u *channelUnitOfWork) Transact(ctx context.Context, fn func(context.Context, port.Repositories) error) error {
	backup := make(map[domain.NotificationChannelID]port.NotificationChannelRecord, len(u.repository.records))
	for id, record := range u.repository.records {
		backup[id] = cloneChannelRecord(record)
	}
	if err := fn(ctx, port.Repositories{NotificationChannels: u.repository}); err != nil {
		u.repository.records = backup
		return err
	}
	return nil
}

func cloneChannelRecord(record port.NotificationChannelRecord) port.NotificationChannelRecord {
	record.EncryptedConfig = append([]byte(nil), record.EncryptedConfig...)
	return record
}
