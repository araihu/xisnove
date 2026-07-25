package application

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
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
	PrincipalAdmin PrincipalKind = "admin"
	PrincipalAgent PrincipalKind = "agent"
)

type Principal struct {
	Kind      PrincipalKind
	SubjectID string
}

type SessionCredential struct {
	Token     string
	ExpiresAt time.Time
}

type AuthServiceConfig struct {
	Store           Store
	Passwords       PasswordHasher
	Tokens          TokenIssuer
	SessionDuration time.Duration
	Now             func() time.Time
	NewID           func() string
}

type AuthService struct {
	store           Store
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

	return s.store.WithinTx(ctx, func(repositories Repositories) error {
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
	admin, err := s.store.Repositories().Admins.FindByEmail(ctx, normalizedEmail)
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
	expiresAt := s.now().UTC().Add(s.sessionDuration)
	err = s.store.Repositories().Sessions.Create(ctx, SessionRecord{
		ID:        s.newID(),
		AdminID:   admin.ID,
		TokenHash: token.Hash,
		ExpiresAt: expiresAt,
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
	session, err := s.store.Repositories().Sessions.FindActiveByTokenHash(
		ctx,
		s.tokens.Hash(rawToken),
		s.now().UTC(),
	)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Principal{}, ErrInvalidCredentials
		}
		return Principal{}, fmt.Errorf("authenticate session: %w", err)
	}
	return Principal{Kind: PrincipalAdmin, SubjectID: session.AdminID}, nil
}

func normalizeEmail(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(normalized)
	if err != nil || address.Address != normalized {
		return "", ErrInvalidEmail
	}
	return normalized, nil
}
