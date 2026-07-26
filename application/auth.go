package application

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrAlreadyBootstrapped = errors.New("administrator already bootstrapped")
	ErrInvalidCredentials  = errors.New("invalid credentials")
	ErrInvalidEmail        = errors.New("invalid email")
	ErrWeakPassword        = errors.New("password must contain at least 16 characters")
)

type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(encodedHash, password string) bool
}

type IssuedToken struct {
	Raw  string
	Hash []byte
}

type TokenIssuer interface {
	New() (IssuedToken, error)
	Hash(raw string) []byte
}

type PrincipalKind string

const (
	PrincipalAdmin    PrincipalKind = "admin"
	PrincipalAPIToken PrincipalKind = "api-token"
	PrincipalAgent    PrincipalKind = "agent"
)

type CredentialKind string

const (
	CredentialSession  CredentialKind = "session"
	CredentialAPIToken CredentialKind = "api-token"
	CredentialAgent    CredentialKind = "agent"
)

type Principal struct {
	Kind           PrincipalKind
	SubjectID      string
	CredentialKind CredentialKind
	CredentialID   string
	Scopes         []Scope
}

type SessionCredential struct {
	Token     string
	ExpiresAt time.Time
}

type AuthServiceConfig struct {
	Store           UnitOfWork
	Passwords       PasswordHasher
	Tokens          TokenIssuer
	SessionDuration time.Duration
	Now             func() time.Time
	NewID           func() string
}

type AuthService struct {
	store           UnitOfWork
	passwords       PasswordHasher
	tokens          TokenIssuer
	sessionDuration time.Duration
	now             func() time.Time
	newID           func() string
}

func NewAuthService(config AuthServiceConfig) *AuthService {
	return &AuthService{
		store:           config.Store,
		passwords:       config.Passwords,
		tokens:          config.Tokens,
		sessionDuration: config.SessionDuration,
		now:             config.Now,
		newID:           config.NewID,
	}
}

func (s *AuthService) BootstrapAdmin(
	ctx context.Context,
	email string,
	password string,
) error {
	normalizedEmail, err := normalizeEmail(email)
	if err != nil {
		return err
	}
	if utf8.RuneCountInString(password) < 16 {
		return ErrWeakPassword
	}

	passwordHash, err := s.passwords.Hash(password)
	if err != nil {
		return fmt.Errorf("hash administrator password: %w", err)
	}
	record := AdminRecord{
		ID:           s.newID(),
		Email:        normalizedEmail,
		PasswordHash: passwordHash,
		CreatedAt:    s.now().UTC(),
	}

	return s.store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		count, err := repositories.Admins.Count(ctx)
		if err != nil {
			return fmt.Errorf("count administrators: %w", err)
		}
		if count != 0 {
			return ErrAlreadyBootstrapped
		}
		if err := repositories.Admins.Create(ctx, record); err != nil {
			return fmt.Errorf("create administrator: %w", err)
		}
		return nil
	})
}

func (s *AuthService) CreateSession(
	ctx context.Context,
	email string,
	password string,
) (SessionCredential, error) {
	normalizedEmail, err := normalizeEmail(email)
	if err != nil {
		return SessionCredential{}, ErrInvalidCredentials
	}
	var admin AdminRecord
	err = s.store.View(ctx, func(ctx context.Context, repositories Repositories) error {
		var findErr error
		admin, findErr = repositories.Admins.FindByEmail(ctx, normalizedEmail)
		return findErr
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return SessionCredential{}, ErrInvalidCredentials
		}
		return SessionCredential{}, fmt.Errorf("find administrator: %w", err)
	}
	if !s.passwords.Verify(admin.PasswordHash, password) {
		return SessionCredential{}, ErrInvalidCredentials
	}

	token, err := s.tokens.New()
	if err != nil {
		return SessionCredential{}, fmt.Errorf("issue session token: %w", err)
	}
	computedHash := s.tokens.Hash(token.Raw)
	if token.Raw == "" || len(token.Hash) == 0 || len(computedHash) != len(token.Hash) ||
		subtle.ConstantTimeCompare(computedHash, token.Hash) != 1 {
		return SessionCredential{}, errors.New("token issuer returned inconsistent credential")
	}
	sessionID := s.newID()
	var expiresAt time.Time
	err = s.store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		databaseNow, err := repositories.Runs.DatabaseNow(ctx)
		if err != nil {
			return fmt.Errorf("read database time for session creation: %w", err)
		}
		expiresAt = databaseNow.UTC().Add(s.sessionDuration)
		return repositories.Sessions.Create(ctx, SessionRecord{
			ID:        sessionID,
			AdminID:   admin.ID,
			TokenHash: token.Hash,
			ExpiresAt: expiresAt,
		})
	})
	if err != nil {
		return SessionCredential{}, fmt.Errorf("create session: %w", err)
	}
	return SessionCredential{Token: token.Raw, ExpiresAt: expiresAt}, nil
}

func (s *AuthService) AuthenticateSession(
	ctx context.Context,
	rawToken string,
) (Principal, error) {
	if rawToken == "" {
		return Principal{}, ErrInvalidCredentials
	}
	var session SessionRecord
	err := s.store.View(ctx, func(ctx context.Context, repositories Repositories) error {
		databaseNow, err := repositories.Runs.DatabaseNow(ctx)
		if err != nil {
			return fmt.Errorf("read database time for session authentication: %w", err)
		}
		var findErr error
		session, findErr = repositories.Sessions.FindActiveByTokenHash(
			ctx,
			s.tokens.Hash(rawToken),
			databaseNow.UTC(),
		)
		return findErr
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Principal{}, ErrInvalidCredentials
		}
		return Principal{}, fmt.Errorf("authenticate session: %w", err)
	}
	return Principal{
		Kind: PrincipalAdmin, SubjectID: session.AdminID,
		CredentialKind: CredentialSession, CredentialID: session.ID,
	}, nil
}

func (s *AuthService) AuthenticateBearer(ctx context.Context, rawToken string) (Principal, error) {
	principal, err := s.AuthenticateSession(ctx, rawToken)
	if err == nil {
		return principal, nil
	}
	if !errors.Is(err, ErrInvalidCredentials) {
		return Principal{}, err
	}
	if rawToken == "" {
		return Principal{}, ErrInvalidCredentials
	}

	var token APITokenRecord
	err = s.store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		databaseNow, err := repositories.Runs.DatabaseNow(ctx)
		if err != nil {
			return fmt.Errorf("read database time for API token authentication: %w", err)
		}
		databaseNow = databaseNow.UTC()
		var findErr error
		token, findErr = repositories.APITokens.FindActiveByTokenHash(ctx, s.tokens.Hash(rawToken), databaseNow)
		if findErr != nil {
			return findErr
		}
		if err := repositories.APITokens.TouchLastUsed(ctx, token.ID, databaseNow); err != nil {
			return fmt.Errorf("touch API token: %w", err)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Principal{}, ErrInvalidCredentials
		}
		return Principal{}, fmt.Errorf("authenticate API token: %w", err)
	}
	return Principal{
		Kind: PrincipalAPIToken, SubjectID: token.AdminID,
		CredentialKind: CredentialAPIToken, CredentialID: token.ID,
		Scopes: slices.Clone(token.Scopes),
	}, nil
}

func (s *AuthService) RevokeCurrentSession(ctx context.Context, principal Principal) error {
	if principal.Kind != PrincipalAdmin || principal.CredentialKind != CredentialSession || principal.CredentialID == "" {
		return ErrInvalidCredentials
	}
	payload, err := json.Marshal(struct {
		SessionID string `json:"sessionId"`
	}{SessionID: principal.CredentialID})
	if err != nil {
		return fmt.Errorf("encode session revocation audit payload: %w", err)
	}
	return s.store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		databaseNow, err := repositories.Runs.DatabaseNow(ctx)
		if err != nil {
			return fmt.Errorf("read database time for session revocation: %w", err)
		}
		databaseNow = databaseNow.UTC()
		revoked, err := repositories.Sessions.Revoke(ctx, principal.CredentialID, databaseNow)
		if err != nil {
			return fmt.Errorf("revoke session: %w", err)
		}
		if !revoked {
			return ErrInvalidCredentials
		}
		if err := repositories.Audit.Append(ctx, AuditEventRecord{
			ID: s.newID(), Kind: "session.revoked", SubjectKind: "session",
			SubjectID: principal.CredentialID, Payload: payload, CreatedAt: databaseNow,
		}); err != nil {
			return fmt.Errorf("audit session revocation: %w", err)
		}
		return nil
	})
}

func normalizeEmail(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(normalized)
	if err != nil || address.Address != normalized {
		return "", ErrInvalidEmail
	}
	return normalized, nil
}
