package application_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/araihu/xisnove/internal/application"
)

const testPassword = "correct horse battery staple"

func TestBootstrapAdminRefusesSecondAdministrator(t *testing.T) {
	service, _ := newAuthServiceForTest()
	ctx := context.Background()
	if err := service.BootstrapAdmin(ctx, "admin@example.com", testPassword); err != nil {
		t.Fatal(err)
	}

	err := service.BootstrapAdmin(
		ctx,
		"other@example.com",
		"another correct horse battery staple",
	)
	if !errors.Is(err, application.ErrAlreadyBootstrapped) {
		t.Fatalf("error = %v", err)
	}
}

func TestCreateAndAuthenticateSession(t *testing.T) {
	service, _ := newAuthServiceForTest()
	ctx := context.Background()
	if err := service.BootstrapAdmin(ctx, " Admin@Example.COM ", testPassword); err != nil {
		t.Fatal(err)
	}

	credential, err := service.CreateSession(ctx, "admin@example.com", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := service.AuthenticateSession(ctx, credential.Token)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Kind != application.PrincipalAdmin {
		t.Fatalf("Kind = %s", principal.Kind)
	}
	if principal.SubjectID != "id-1" {
		t.Fatalf("SubjectID = %q", principal.SubjectID)
	}
}

func TestCreateSessionDoesNotPersistRawToken(t *testing.T) {
	service, store := newAuthServiceForTest()
	ctx := context.Background()
	if err := service.BootstrapAdmin(ctx, "admin@example.com", testPassword); err != nil {
		t.Fatal(err)
	}

	credential, err := service.CreateSession(ctx, "admin@example.com", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.sessions.records) != 1 {
		t.Fatalf("sessions = %d", len(store.sessions.records))
	}
	if bytes.Equal(store.sessions.records[0].TokenHash, []byte(credential.Token)) {
		t.Fatal("raw token was persisted")
	}
	want := sha256.Sum256([]byte(credential.Token))
	if !bytes.Equal(store.sessions.records[0].TokenHash, want[:]) {
		t.Fatal("stored token hash does not match the issued credential")
	}
}

func TestCreateSessionUsesSameErrorForUnknownEmailAndBadPassword(t *testing.T) {
	service, _ := newAuthServiceForTest()
	ctx := context.Background()
	if err := service.BootstrapAdmin(ctx, "admin@example.com", testPassword); err != nil {
		t.Fatal(err)
	}

	_, missingErr := service.CreateSession(ctx, "missing@example.com", testPassword)
	_, passwordErr := service.CreateSession(ctx, "admin@example.com", "incorrect password value")
	if !errors.Is(missingErr, application.ErrInvalidCredentials) {
		t.Fatalf("missing email error = %v", missingErr)
	}
	if !errors.Is(passwordErr, application.ErrInvalidCredentials) {
		t.Fatalf("bad password error = %v", passwordErr)
	}
}

func newAuthServiceForTest() (*application.AuthService, *fakeStore) {
	store := newFakeStore()
	now := time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC)
	nextID := 0
	service := application.NewAuthService(application.AuthServiceConfig{
		Store:           store,
		Passwords:       testPasswordHasher{},
		Tokens:          testTokenIssuer{},
		SessionDuration: 12 * time.Hour,
		Now:             func() time.Time { return now },
		NewID: func() string {
			nextID++
			return fmt.Sprintf("id-%d", nextID)
		},
	})
	return service, store
}

type testPasswordHasher struct{}

func (testPasswordHasher) Hash(password string) (string, error) {
	return "hash:" + password, nil
}

func (testPasswordHasher) Verify(encodedHash, password string) bool {
	return encodedHash == "hash:"+password
}

type testTokenIssuer struct{}

func (testTokenIssuer) New() (application.IssuedToken, error) {
	raw := "0123456789012345678901234567890123456789012"
	sum := sha256.Sum256([]byte(raw))
	return application.IssuedToken{Raw: raw, Hash: sum[:]}, nil
}

func (testTokenIssuer) Hash(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

type fakeStore struct {
	mu       sync.Mutex
	admins   *fakeAdminRepository
	sessions *fakeSessionRepository
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		admins:   &fakeAdminRepository{},
		sessions: &fakeSessionRepository{},
	}
}

func (s *fakeStore) Repositories() application.Repositories {
	return application.Repositories{Admins: s.admins, Sessions: s.sessions}
}

func (s *fakeStore) WithinTx(
	_ context.Context,
	fn func(application.Repositories) error,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(s.Repositories())
}

type fakeAdminRepository struct {
	records []application.AdminRecord
}

func (r *fakeAdminRepository) Count(context.Context) (int64, error) {
	return int64(len(r.records)), nil
}

func (r *fakeAdminRepository) Create(_ context.Context, record application.AdminRecord) error {
	r.records = append(r.records, record)
	return nil
}

func (r *fakeAdminRepository) FindByEmail(
	_ context.Context,
	email string,
) (application.AdminRecord, error) {
	for _, record := range r.records {
		if record.Email == email {
			return record, nil
		}
	}
	return application.AdminRecord{}, application.ErrNotFound
}

type fakeSessionRepository struct {
	records []application.SessionRecord
}

func (r *fakeSessionRepository) Create(
	_ context.Context,
	record application.SessionRecord,
) error {
	record.TokenHash = bytes.Clone(record.TokenHash)
	r.records = append(r.records, record)
	return nil
}

func (r *fakeSessionRepository) FindActiveByTokenHash(
	_ context.Context,
	hash []byte,
	now time.Time,
) (application.SessionRecord, error) {
	for _, record := range r.records {
		if bytes.Equal(record.TokenHash, hash) &&
			record.ExpiresAt.After(now) &&
			record.RevokedAt == nil {
			return record, nil
		}
	}
	return application.SessionRecord{}, application.ErrNotFound
}
