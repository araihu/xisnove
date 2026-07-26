package application_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
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

func TestAuthenticateAPITokenRejectsExpiredAndRevoked(t *testing.T) {
	store := newFakeStore()
	now := time.Date(2026, 7, 26, 4, 0, 0, 0, time.UTC)
	store.runs.now = now
	activeRaw, expiredRaw, revokedRaw := "active-token", "expired-token", "revoked-token"
	revokedAt := now.Add(-time.Hour)
	expiredAt := now.Add(-time.Minute)
	store.apiTokens.records = []application.APITokenRecord{
		{
			ID: "active-id", AdminID: "admin-id", Label: "active", TokenHash: testTokenIssuer{}.Hash(activeRaw),
			Scopes: []application.Scope{application.ScopeMonitorsRead}, CreatedAt: now.Add(-time.Hour),
		},
		{
			ID: "expired-id", AdminID: "admin-id", Label: "expired", TokenHash: testTokenIssuer{}.Hash(expiredRaw),
			Scopes: []application.Scope{application.ScopeMonitorsRead}, CreatedAt: now.Add(-time.Hour), ExpiresAt: &expiredAt,
		},
		{
			ID: "revoked-id", AdminID: "admin-id", Label: "revoked", TokenHash: testTokenIssuer{}.Hash(revokedRaw),
			Scopes: []application.Scope{application.ScopeMonitorsRead}, CreatedAt: now.Add(-time.Hour), RevokedAt: &revokedAt,
		},
	}
	service := application.NewAuthService(application.AuthServiceConfig{
		Store: store, Tokens: testTokenIssuer{}, Now: func() time.Time { return now },
	})

	principal, err := service.AuthenticateBearer(context.Background(), activeRaw)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Kind != application.PrincipalAPIToken || principal.SubjectID != "admin-id" ||
		principal.CredentialKind != application.CredentialAPIToken || principal.CredentialID != "active-id" ||
		!slices.Equal(principal.Scopes, []application.Scope{application.ScopeMonitorsRead}) {
		t.Fatalf("principal = %#v", principal)
	}
	if store.apiTokens.records[0].LastUsedAt == nil || !store.apiTokens.records[0].LastUsedAt.Equal(now) {
		t.Fatal("successful API token authentication did not update last-used time")
	}
	for _, raw := range []string{expiredRaw, revokedRaw, "unknown-token"} {
		if _, err := service.AuthenticateBearer(context.Background(), raw); !errors.Is(err, application.ErrInvalidCredentials) {
			t.Errorf("AuthenticateBearer(%q) error = %v", raw, err)
		}
	}
}

func TestRevokeCurrentSessionInvalidatesCredential(t *testing.T) {
	service, store := newAuthServiceForTest()
	ctx := context.Background()
	if err := service.BootstrapAdmin(ctx, "admin@example.com", testPassword); err != nil {
		t.Fatal(err)
	}
	credential, err := service.CreateSession(ctx, "admin@example.com", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := service.AuthenticateBearer(ctx, credential.Token)
	if err != nil {
		t.Fatal(err)
	}
	if principal.CredentialID == "" || principal.CredentialKind != application.CredentialSession {
		t.Fatalf("principal = %#v", principal)
	}
	if err := service.RevokeCurrentSession(ctx, principal); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthenticateBearer(ctx, credential.Token); !errors.Is(err, application.ErrInvalidCredentials) {
		t.Fatalf("authenticate revoked session error = %v", err)
	}
	if len(store.audit.records) != 1 || store.audit.records[0].Kind != "session.revoked" {
		t.Fatalf("audit = %#v", store.audit.records)
	}
	if bytes.Contains(store.audit.records[0].Payload, []byte(credential.Token)) {
		t.Fatalf("audit leaked raw session token: %s", store.audit.records[0].Payload)
	}
}

func TestRevokeCurrentSessionAuditFailureRollsBackCredential(t *testing.T) {
	service, store := newAuthServiceForTest()
	ctx := context.Background()
	if err := service.BootstrapAdmin(ctx, "admin@example.com", testPassword); err != nil {
		t.Fatal(err)
	}
	credential, err := service.CreateSession(ctx, "admin@example.com", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := service.AuthenticateBearer(ctx, credential.Token)
	if err != nil {
		t.Fatal(err)
	}
	store.audit.err = errors.New("audit unavailable")
	if err := service.RevokeCurrentSession(ctx, principal); err == nil {
		t.Fatal("RevokeCurrentSession() succeeded despite audit failure")
	}
	if _, err := service.AuthenticateBearer(ctx, credential.Token); err != nil {
		t.Fatalf("rolled-back session is not active: %v", err)
	}
}

func TestAuthenticateBearerDoesNotHideSessionStoreFailure(t *testing.T) {
	store := newFakeStore()
	store.sessions.findErr = errors.New("store unavailable")
	service := application.NewAuthService(application.AuthServiceConfig{
		Store: store, Tokens: testTokenIssuer{}, Now: time.Now,
	})
	_, err := service.AuthenticateBearer(context.Background(), "credential")
	if err == nil || errors.Is(err, application.ErrInvalidCredentials) {
		t.Fatalf("AuthenticateBearer() error = %v", err)
	}
}

func TestCreateAndRevokeSessionUseDatabaseTimeDespiteApplicationClockSkew(t *testing.T) {
	store := newFakeStore()
	databaseNow := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	store.runs.now = databaseNow
	applicationNow := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	service := application.NewAuthService(application.AuthServiceConfig{
		Store: store, Passwords: testPasswordHasher{}, Tokens: testTokenIssuer{},
		SessionDuration: 12 * time.Hour, Now: func() time.Time { return applicationNow },
		NewID: func() string { return "credential-id" },
	})
	ctx := context.Background()
	if err := service.BootstrapAdmin(ctx, "admin@example.com", testPassword); err != nil {
		t.Fatal(err)
	}
	credential, err := service.CreateSession(ctx, "admin@example.com", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if want := databaseNow.Add(12 * time.Hour); !credential.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %s, want database time %s", credential.ExpiresAt, want)
	}
	principal, err := service.AuthenticateBearer(ctx, credential.Token)
	if err != nil {
		t.Fatal(err)
	}
	store.runs.now = databaseNow.Add(time.Minute)
	if err := service.RevokeCurrentSession(ctx, principal); err != nil {
		t.Fatal(err)
	}
	if got := store.sessions.records[0].RevokedAt; got == nil || !got.Equal(store.runs.now) {
		t.Fatalf("RevokedAt = %v, want database time %s", got, store.runs.now)
	}
	if got := store.audit.records[0].CreatedAt; !got.Equal(store.runs.now) {
		t.Fatalf("audit CreatedAt = %s, want database time %s", got, store.runs.now)
	}
}

func TestAPITokenLifecycleUsesDatabaseTimeDespiteApplicationClockSkew(t *testing.T) {
	store := newFakeStore()
	databaseNow := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	store.runs.now = databaseNow
	applicationNow := time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC)
	tokens := testTokenIssuer{}
	service := application.NewAPITokenService(application.APITokenServiceConfig{
		Store: store, Tokens: tokens, Now: func() time.Time { return applicationNow },
		NewID: func() string { return "token-id" },
	})
	expiresAt := databaseNow.Add(time.Hour)
	credential, err := service.Create(context.Background(), application.Principal{
		Kind: application.PrincipalAdmin, SubjectID: "admin-id",
	}, application.CreateAPITokenCommand{
		Label: "skew", Scopes: []application.Scope{application.ScopeMonitorsRead}, ExpiresAt: &expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := store.apiTokens.records[0].CreatedAt; !got.Equal(databaseNow) {
		t.Fatalf("CreatedAt = %s, want database time %s", got, databaseNow)
	}
	auth := application.NewAuthService(application.AuthServiceConfig{
		Store: store, Tokens: tokens, Now: func() time.Time { return applicationNow }, NewID: func() string { return "audit-id" },
	})
	principal, err := auth.AuthenticateBearer(context.Background(), credential.Token)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.apiTokens.records[0].LastUsedAt; got == nil || !got.Equal(databaseNow) {
		t.Fatalf("LastUsedAt = %v, want database time %s", got, databaseNow)
	}
	store.runs.now = databaseNow.Add(time.Minute)
	if err := service.Revoke(context.Background(), application.Principal{
		Kind: application.PrincipalAdmin, SubjectID: principal.SubjectID,
	}, "token-id"); err != nil {
		t.Fatal(err)
	}
	if got := store.apiTokens.records[0].RevokedAt; got == nil || !got.Equal(store.runs.now) {
		t.Fatalf("RevokedAt = %v, want database time %s", got, store.runs.now)
	}
	if got := store.audit.records[len(store.audit.records)-1].CreatedAt; !got.Equal(store.runs.now) {
		t.Fatalf("audit CreatedAt = %s, want database time %s", got, store.runs.now)
	}
}

func TestAuthenticateAPITokenUsesDatabaseTimeWhenApplicationClockIsBehind(t *testing.T) {
	store := newFakeStore()
	databaseNow := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	store.runs.now = databaseNow
	expiredAt := databaseNow.Add(-time.Second)
	store.apiTokens.records = []application.APITokenRecord{{
		ID: "expired-id", AdminID: "admin-id", Label: "expired", TokenHash: testTokenIssuer{}.Hash("expired-token"),
		Scopes: []application.Scope{application.ScopeMonitorsRead}, CreatedAt: databaseNow.Add(-time.Hour), ExpiresAt: &expiredAt,
	}}
	service := application.NewAuthService(application.AuthServiceConfig{
		Store: store, Tokens: testTokenIssuer{},
		Now: func() time.Time { return time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	if _, err := service.AuthenticateBearer(context.Background(), "expired-token"); !errors.Is(err, application.ErrInvalidCredentials) {
		t.Fatalf("AuthenticateBearer() error = %v, want ErrInvalidCredentials", err)
	}
}

func newAuthServiceForTest() (*application.AuthService, *fakeStore) {
	store := newFakeStore()
	now := time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC)
	store.runs.now = now
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
	mu        sync.Mutex
	admins    *fakeAdminRepository
	sessions  *fakeSessionRepository
	apiTokens *fakeAPITokenRepository
	audit     *fakeAuditRepository
	runs      *fakeRunRepository
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		admins:    &fakeAdminRepository{},
		sessions:  &fakeSessionRepository{},
		apiTokens: &fakeAPITokenRepository{},
		audit:     &fakeAuditRepository{},
		runs:      &fakeRunRepository{now: time.Date(2026, 7, 26, 3, 0, 0, 0, time.UTC)},
	}
}

func (s *fakeStore) Repositories() application.Repositories {
	return application.Repositories{
		Admins: s.admins, Sessions: s.sessions, APITokens: s.apiTokens, Audit: s.audit, Runs: s.runs,
	}
}

func (s *fakeStore) View(
	ctx context.Context,
	fn func(context.Context, application.Repositories) error,
) error {
	return fn(ctx, s.Repositories())
}

func (s *fakeStore) Transact(
	ctx context.Context,
	fn func(context.Context, application.Repositories) error,
) error {
	return s.WithinTx(ctx, func(repositories application.Repositories) error {
		return fn(ctx, repositories)
	})
}

func (s *fakeStore) WithinTx(
	_ context.Context,
	fn func(application.Repositories) error,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	admins := append([]application.AdminRecord(nil), s.admins.records...)
	sessions := append([]application.SessionRecord(nil), s.sessions.records...)
	apiTokens := append([]application.APITokenRecord(nil), s.apiTokens.records...)
	audit := append([]application.AuditEventRecord(nil), s.audit.records...)
	if err := fn(s.Repositories()); err != nil {
		s.admins.records = admins
		s.sessions.records = sessions
		s.apiTokens.records = apiTokens
		s.audit.records = audit
		return err
	}
	return nil
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
	findErr error
}

type fakeRunRepository struct {
	now time.Time
	err error
}

func (r *fakeRunRepository) DatabaseNow(context.Context) (time.Time, error) { return r.now, r.err }
func (*fakeRunRepository) Insert(context.Context, application.NewRunRecord) (bool, error) {
	return false, nil
}
func (*fakeRunRepository) ClaimProbe(context.Context, application.ClaimRunParams) (application.RunRecord, error) {
	return application.RunRecord{}, nil
}
func (*fakeRunRepository) Get(context.Context, domain.CheckRunID) (application.RunRecord, error) {
	return application.RunRecord{}, nil
}
func (*fakeRunRepository) Resolve(context.Context, domain.CheckRunID, domain.AgentID, []byte, time.Time) (bool, error) {
	return false, nil
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
	if r.findErr != nil {
		return application.SessionRecord{}, r.findErr
	}
	for _, record := range r.records {
		if bytes.Equal(record.TokenHash, hash) &&
			record.ExpiresAt.After(now) &&
			record.RevokedAt == nil {
			return record, nil
		}
	}
	return application.SessionRecord{}, application.ErrNotFound
}

func (r *fakeSessionRepository) Revoke(_ context.Context, id string, at time.Time) (bool, error) {
	for i := range r.records {
		if r.records[i].ID == id && r.records[i].RevokedAt == nil {
			r.records[i].RevokedAt = &at
			return true, nil
		}
	}
	return false, nil
}
