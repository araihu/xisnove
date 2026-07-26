package contracttest

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

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
