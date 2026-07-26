package contracttest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	appservice "github.com/araihu/xisnove/application"
	application "github.com/araihu/xisnove/application/port"
)

const humanClientAdminID = "00000000-0000-4000-8000-000000000101"

func seedHumanClientAdmin(t *testing.T, store application.UnitOfWork) {
	t.Helper()
	transact(t, context.Background(), store, func(ctx context.Context, repositories application.Repositories) error {
		return repositories.Admins.Create(ctx, application.AdminRecord{
			ID: humanClientAdminID, Email: "auth-contract@example.com",
			PasswordHash: "not-a-real-password-hash", CreatedAt: time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC),
		})
	})
}

func testAPITokenHashOnly(t *testing.T, store application.UnitOfWork) {
	seedHumanClientAdmin(t, store)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 1, 0, 0, 0, time.UTC)
	hash := []byte("01234567890123456789012345678901")
	transact(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		return repositories.APITokens.Create(ctx, application.APITokenRecord{
			ID: "00000000-0000-4000-8000-000000000102", AdminID: humanClientAdminID,
			Label: "deploy", TokenHash: hash, Scopes: []application.Scope{application.ScopeMonitorsRead}, CreatedAt: now,
		})
	})
	view(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		record, err := repositories.APITokens.FindActiveByTokenHash(ctx, hash, now.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(record.TokenHash, hash) || record.Label != "deploy" {
			t.Fatalf("record = %#v", record)
		}
		record.Scopes[0] = application.ScopeTokensWrite
		again, err := repositories.APITokens.FindActiveByTokenHash(ctx, hash, now.Add(time.Second))
		if err != nil || again.Scopes[0] != application.ScopeMonitorsRead {
			t.Fatalf("scope boundary aliases storage: %#v, %v", again, err)
		}
		return nil
	})
	duplicateErr := store.Transact(ctx, func(ctx context.Context, repositories application.Repositories) error {
		return repositories.APITokens.Create(ctx, application.APITokenRecord{
			ID: "00000000-0000-4000-8000-000000000103", AdminID: humanClientAdminID,
			Label: "duplicate", TokenHash: hash, Scopes: []application.Scope{application.ScopeTokensRead}, CreatedAt: now,
		})
	})
	if !errors.Is(duplicateErr, application.ErrConflict) {
		t.Fatalf("duplicate token hash error = %v, want ErrConflict", duplicateErr)
	}
}

func testSessionRevocation(t *testing.T, store application.UnitOfWork) {
	seedHumanClientAdmin(t, store)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 1, 0, 0, 0, time.UTC)
	hash := []byte("session-hash")
	transact(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		if err := repositories.Sessions.Create(ctx, application.SessionRecord{
			ID: "00000000-0000-4000-8000-000000000104", AdminID: humanClientAdminID,
			TokenHash: hash, ExpiresAt: now.Add(time.Hour),
		}); err != nil {
			return err
		}
		revoked, err := repositories.Sessions.Revoke(ctx, "00000000-0000-4000-8000-000000000104", now)
		if err != nil || !revoked {
			t.Fatalf("Revoke() = %v, %v", revoked, err)
		}
		return nil
	})
	view(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		_, err := repositories.Sessions.FindActiveByTokenHash(ctx, hash, now)
		if !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("revoked session lookup error = %v", err)
		}
		return nil
	})
}

func testAPITokenExpiryAndRevocation(t *testing.T, store application.UnitOfWork) {
	seedHumanClientAdmin(t, store)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 1, 0, 0, 0, time.UTC)
	expiredAt := now.Add(-time.Second)
	transact(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		for _, record := range []application.APITokenRecord{
			{ID: "00000000-0000-4000-8000-000000000105", AdminID: humanClientAdminID, Label: "expired", TokenHash: []byte("expired-hash"), Scopes: []application.Scope{application.ScopeStatusRead}, CreatedAt: now.Add(-time.Hour), ExpiresAt: &expiredAt},
			{ID: "00000000-0000-4000-8000-000000000106", AdminID: humanClientAdminID, Label: "active", TokenHash: []byte("active-hash"), Scopes: []application.Scope{application.ScopeStatusRead}, CreatedAt: now},
		} {
			if err := repositories.APITokens.Create(ctx, record); err != nil {
				return err
			}
		}
		return nil
	})
	view(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		if _, err := repositories.APITokens.FindActiveByTokenHash(ctx, []byte("expired-hash"), now); !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("expired lookup error = %v", err)
		}
		return nil
	})
	transact(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		revoked, err := repositories.APITokens.Revoke(ctx, "00000000-0000-4000-8000-000000000106", now)
		if err != nil || !revoked {
			t.Fatalf("Revoke() = %v, %v", revoked, err)
		}
		return nil
	})
	view(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		_, err := repositories.APITokens.FindActiveByTokenHash(ctx, []byte("active-hash"), now)
		if !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("revoked lookup error = %v", err)
		}
		return nil
	})
}

func testAPITokenCursorOrdering(t *testing.T, store application.UnitOfWork) {
	seedHumanClientAdmin(t, store)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 1, 0, 0, 0, time.UTC)
	transact(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		for i, id := range []string{
			"00000000-0000-4000-8000-000000000109",
			"00000000-0000-4000-8000-000000000107",
			"00000000-0000-4000-8000-000000000108",
		} {
			if err := repositories.APITokens.Create(ctx, application.APITokenRecord{
				ID: id, AdminID: humanClientAdminID, Label: id, TokenHash: []byte(id),
				Scopes: []application.Scope{application.ScopeTokensRead}, CreatedAt: now.Add(time.Duration(i/2) * time.Second),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	var first application.Page[application.APITokenRecord]
	view(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		var err error
		first, err = repositories.APITokens.List(ctx, application.PageRequest{Limit: 2})
		return err
	})
	if len(first.Items) != 2 || first.NextCursor == "" || first.Items[0].ID != "00000000-0000-4000-8000-000000000107" || first.Items[1].ID != "00000000-0000-4000-8000-000000000109" {
		t.Fatalf("first page = %#v", first)
	}
	view(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		second, err := repositories.APITokens.List(ctx, application.PageRequest{Limit: 2, Cursor: first.NextCursor})
		if err != nil {
			return err
		}
		if len(second.Items) != 1 || second.Items[0].ID != "00000000-0000-4000-8000-000000000108" || second.NextCursor != "" {
			t.Fatalf("second page = %#v", second)
		}
		return nil
	})
}

func testHumanCredentialDatabaseClockSkew(t *testing.T, store application.UnitOfWork) {
	seedHumanClientAdmin(t, store)
	ctx := context.Background()
	var databaseNow time.Time
	view(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		var err error
		databaseNow, err = repositories.Runs.DatabaseNow(ctx)
		return err
	})
	ids := newHumanClientIDs()
	tokens := contractTokenIssuer{}
	auth := appservice.NewAuthService(appservice.AuthServiceConfig{
		Store: store, Passwords: contractPasswordHasher{}, Tokens: tokens,
		SessionDuration: time.Hour, Now: func() time.Time { return time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC) },
		NewID: ids,
	})
	session, err := auth.CreateSession(ctx, "auth-contract@example.com", "password")
	if err != nil {
		t.Fatal(err)
	}
	if session.ExpiresAt.Before(databaseNow.Add(59*time.Minute)) || session.ExpiresAt.After(databaseNow.Add(61*time.Minute)) {
		t.Fatalf("session expiry = %s, want database time + 1h near %s", session.ExpiresAt, databaseNow.Add(time.Hour))
	}
	principal, err := auth.AuthenticateBearer(ctx, session.Token)
	if err != nil {
		t.Fatal(err)
	}

	apiTokens := appservice.NewAPITokenService(appservice.APITokenServiceConfig{
		Store: store, Tokens: tokens,
		Now: func() time.Time { return time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC) }, NewID: ids,
	})
	expiresAt := databaseNow.Add(2 * time.Hour)
	credential, err := apiTokens.Create(ctx, principal, appservice.CreateAPITokenCommand{
		Label: "clock-skew", Scopes: []appservice.Scope{appservice.ScopeMonitorsRead}, ExpiresAt: &expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if credential.Record.CreatedAt.Before(databaseNow.Add(-time.Second)) || credential.Record.CreatedAt.After(databaseNow.Add(time.Minute)) {
		t.Fatalf("API token CreatedAt = %s, want database time near %s", credential.Record.CreatedAt, databaseNow)
	}
	if _, err := auth.AuthenticateBearer(ctx, credential.Token); err != nil {
		t.Fatal(err)
	}
	page, err := apiTokens.List(ctx, principal, appservice.PageRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var lastUsedAt *time.Time
	for _, record := range page.Items {
		if record.ID == credential.Record.ID {
			lastUsedAt = record.LastUsedAt
		}
	}
	if lastUsedAt == nil || lastUsedAt.Before(databaseNow.Add(-time.Second)) || lastUsedAt.After(databaseNow.Add(time.Minute)) {
		t.Fatalf("LastUsedAt = %v, want database time near %s", lastUsedAt, databaseNow)
	}

	expiredAt := databaseNow.Add(-time.Second)
	expiredHash := tokens.Hash("expired-under-skew")
	transact(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		return repositories.APITokens.Create(ctx, application.APITokenRecord{
			ID: ids(), AdminID: humanClientAdminID, Label: "expired", TokenHash: expiredHash,
			Scopes: []application.Scope{application.ScopeMonitorsRead}, CreatedAt: databaseNow.Add(-time.Hour), ExpiresAt: &expiredAt,
		})
	})
	if _, err := auth.AuthenticateBearer(ctx, "expired-under-skew"); !errors.Is(err, appservice.ErrInvalidCredentials) {
		t.Fatalf("expired token authentication error = %v, want ErrInvalidCredentials", err)
	}
}

func testConcurrentDuplicateAPITokenHash(t *testing.T, store application.UnitOfWork) {
	seedHumanClientAdmin(t, store)
	ctx := context.Background()
	var databaseNow time.Time
	view(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		var err error
		databaseNow, err = repositories.Runs.DatabaseNow(ctx)
		return err
	})
	start := make(chan struct{})
	errorsByWriter := make(chan error, 2)
	for i, id := range []string{
		"00000000-0000-4000-8000-000000000120",
		"00000000-0000-4000-8000-000000000121",
	} {
		go func(i int, id string) {
			<-start
			errorsByWriter <- store.Transact(ctx, func(ctx context.Context, repositories application.Repositories) error {
				return repositories.APITokens.Create(ctx, application.APITokenRecord{
					ID: id, AdminID: humanClientAdminID, Label: fmt.Sprintf("writer-%d", i),
					TokenHash: []byte("same-concurrent-token-hash"),
					Scopes:    []application.Scope{application.ScopeTokensRead}, CreatedAt: databaseNow,
				})
			})
		}(i, id)
	}
	close(start)
	var successes, conflicts int
	for range 2 {
		err := <-errorsByWriter
		switch {
		case err == nil:
			successes++
		case errors.Is(err, application.ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent Create() error = %v, want nil or ErrConflict", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent duplicate results = %d successes, %d conflicts", successes, conflicts)
	}
}

type contractPasswordHasher struct{}

func (contractPasswordHasher) Hash(password string) (string, error) {
	return "contract:" + password, nil
}
func (contractPasswordHasher) Verify(_ string, password string) bool { return password == "password" }

type contractTokenIssuer struct{}

func (contractTokenIssuer) New() (appservice.IssuedToken, error) {
	raw := fmt.Sprintf("contract-token-%d", contractTokenSequence.Add(1))
	hash := sha256.Sum256([]byte(raw))
	return appservice.IssuedToken{Raw: raw, Hash: hash[:]}, nil
}
func (contractTokenIssuer) Hash(raw string) []byte {
	hash := sha256.Sum256([]byte(raw))
	return hash[:]
}

var contractTokenSequence atomic.Uint64

func newHumanClientIDs() func() string {
	var sequence atomic.Uint64
	return func() string {
		return fmt.Sprintf("30000000-0000-4000-8000-%012d", sequence.Add(1))
	}
}
