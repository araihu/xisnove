package application_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
)

func TestTokenScopesAreSortedUniqueAndRecognized(t *testing.T) {
	input := []application.Scope{
		application.ScopeMonitorsWrite,
		application.ScopeTokensRead,
	}
	got, err := application.NormalizeScopes(input)
	if err != nil {
		t.Fatal(err)
	}
	want := []application.Scope{application.ScopeMonitorsWrite, application.ScopeTokensRead}
	if !slices.Equal(got, want) {
		t.Fatalf("NormalizeScopes() = %v, want %v", got, want)
	}
	input[0] = application.ScopeStatusRead
	if !slices.Equal(got, want) {
		t.Fatal("normalized scopes alias caller input")
	}
	for name, scopes := range map[string][]application.Scope{
		"empty":     nil,
		"duplicate": {application.ScopeTokensRead, application.ScopeTokensRead},
		"unknown":   {application.Scope("root:all")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := application.NormalizeScopes(scopes); !errors.Is(err, application.ErrInvalidScopes) {
				t.Fatalf("error = %v, want ErrInvalidScopes", err)
			}
		})
	}
}

func TestCreateAPITokenDisclosesRawValueOnceAndPersistsOnlyHash(t *testing.T) {
	store := newFakeStore()
	now := time.Date(2026, 7, 26, 3, 0, 0, 0, time.UTC)
	service := application.NewAPITokenService(application.APITokenServiceConfig{
		Store: store, Tokens: testTokenIssuer{}, Now: func() time.Time { return now },
		NewID: func() string { return "token-id" },
	})
	principal := application.Principal{
		Kind: application.PrincipalAdmin, SubjectID: "admin-id",
		CredentialKind: application.CredentialSession, CredentialID: "session-id",
	}
	expiresAt := now.Add(24 * time.Hour)

	credential, err := service.Create(context.Background(), principal, application.CreateAPITokenCommand{
		Label: "deploy", Scopes: []application.Scope{
			application.ScopeMonitorsWrite, application.ScopeMonitorsRead,
		}, ExpiresAt: &expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if credential.Token == "" || credential.Record.ID != "token-id" {
		t.Fatalf("credential = %#v", credential)
	}
	if len(store.apiTokens.records) != 1 {
		t.Fatalf("stored tokens = %d", len(store.apiTokens.records))
	}
	record := store.apiTokens.records[0]
	if bytes.Equal(record.TokenHash, []byte(credential.Token)) {
		t.Fatal("raw API token was persisted")
	}
	wantHash := sha256.Sum256([]byte(credential.Token))
	if !bytes.Equal(record.TokenHash, wantHash[:]) {
		t.Fatalf("persisted hash = %x, want %x", record.TokenHash, wantHash)
	}
	if len(store.audit.records) != 1 || store.audit.records[0].Kind != "api-token.created" {
		t.Fatalf("audit = %#v", store.audit.records)
	}
	payload := store.audit.records[0].Payload
	if bytes.Contains(payload, []byte(credential.Token)) || bytes.Contains(payload, []byte(hex.EncodeToString(record.TokenHash))) {
		t.Fatalf("audit payload contains credential material: %s", payload)
	}
}

func TestCreateAPITokenAuditFailureRollsBackCredential(t *testing.T) {
	store := newFakeStore()
	store.audit.err = errors.New("audit unavailable")
	service := application.NewAPITokenService(application.APITokenServiceConfig{
		Store: store, Tokens: testTokenIssuer{},
		Now:   func() time.Time { return time.Date(2026, 7, 26, 3, 0, 0, 0, time.UTC) },
		NewID: func() string { return "token-id" },
	})
	_, err := service.Create(context.Background(), application.Principal{
		Kind: application.PrincipalAdmin, SubjectID: "admin-id",
	}, application.CreateAPITokenCommand{
		Label: "deploy", Scopes: []application.Scope{application.ScopeMonitorsRead},
	})
	if err == nil {
		t.Fatal("Create() succeeded despite audit failure")
	}
	if len(store.apiTokens.records) != 0 {
		t.Fatalf("credential mutation escaped failed transaction: %#v", store.apiTokens.records)
	}
}

func TestRevokeAPITokenAuditsAndRollsBackOnAuditFailure(t *testing.T) {
	store := newFakeStore()
	now := time.Date(2026, 7, 26, 3, 0, 0, 0, time.UTC)
	store.apiTokens.records = []application.APITokenRecord{{
		ID: "token-id", AdminID: "admin-id", Label: "deploy", TokenHash: []byte("hash"),
		Scopes: []application.Scope{application.ScopeMonitorsRead}, CreatedAt: now.Add(-time.Hour),
	}}
	service := application.NewAPITokenService(application.APITokenServiceConfig{
		Store: store, Tokens: testTokenIssuer{}, Now: func() time.Time { return now }, NewID: func() string { return "audit-id" },
	})
	principal := application.Principal{Kind: application.PrincipalAdmin, SubjectID: "admin-id"}
	store.audit.err = errors.New("audit unavailable")
	if err := service.Revoke(context.Background(), principal, "token-id"); err == nil {
		t.Fatal("Revoke() succeeded despite audit failure")
	}
	if store.apiTokens.records[0].RevokedAt != nil {
		t.Fatal("revocation escaped failed transaction")
	}
	store.audit.err = nil
	if err := service.Revoke(context.Background(), principal, "token-id"); err != nil {
		t.Fatal(err)
	}
	if store.apiTokens.records[0].RevokedAt == nil || len(store.audit.records) != 1 || store.audit.records[0].Kind != "api-token.revoked" {
		t.Fatalf("token = %#v, audit = %#v", store.apiTokens.records[0], store.audit.records)
	}
}

func TestAuthorizationMapDeniesUnknownOperation(t *testing.T) {
	principal := application.Principal{
		Kind:   application.PrincipalAPIToken,
		Scopes: []application.Scope{application.ScopeMonitorsRead},
	}
	if err := application.Authorize("listMonitors", principal); err != nil {
		t.Fatalf("known scoped operation: %v", err)
	}
	for _, operationID := range []string{"createMonitor", "unknownOperation", ""} {
		if err := application.Authorize(operationID, principal); !errors.Is(err, application.ErrForbidden) {
			t.Errorf("Authorize(%q) error = %v, want ErrForbidden", operationID, err)
		}
	}
	admin := application.Principal{Kind: application.PrincipalAdmin}
	if err := application.Authorize("listMonitors", admin); err != nil {
		t.Fatalf("admin known operation: %v", err)
	}
	if err := application.Authorize("unknownOperation", admin); !errors.Is(err, application.ErrForbidden) {
		t.Fatalf("admin unknown operation error = %v", err)
	}
}

type fakeAPITokenRepository struct {
	records []application.APITokenRecord
}

func (r *fakeAPITokenRepository) Create(_ context.Context, record application.APITokenRecord) error {
	record.TokenHash = bytes.Clone(record.TokenHash)
	record.Scopes = slices.Clone(record.Scopes)
	r.records = append(r.records, record)
	return nil
}

func (r *fakeAPITokenRepository) FindActiveByTokenHash(_ context.Context, hash []byte, now time.Time) (application.APITokenRecord, error) {
	for _, record := range r.records {
		if bytes.Equal(record.TokenHash, hash) && record.RevokedAt == nil &&
			(record.ExpiresAt == nil || record.ExpiresAt.After(now)) {
			record.TokenHash = bytes.Clone(record.TokenHash)
			record.Scopes = slices.Clone(record.Scopes)
			return record, nil
		}
	}
	return application.APITokenRecord{}, application.ErrNotFound
}

func (r *fakeAPITokenRepository) List(context.Context, application.PageRequest) (application.Page[application.APITokenRecord], error) {
	return application.Page[application.APITokenRecord]{Items: slices.Clone(r.records)}, nil
}

func (r *fakeAPITokenRepository) Revoke(_ context.Context, id string, at time.Time) (bool, error) {
	for i := range r.records {
		if r.records[i].ID == id && r.records[i].RevokedAt == nil {
			r.records[i].RevokedAt = &at
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeAPITokenRepository) TouchLastUsed(_ context.Context, id string, at time.Time) error {
	for i := range r.records {
		if r.records[i].ID == id {
			r.records[i].LastUsedAt = &at
			return nil
		}
	}
	return application.ErrNotFound
}

type fakeAuditRepository struct {
	records []application.AuditEventRecord
	err     error
}

func (r *fakeAuditRepository) Append(_ context.Context, record application.AuditEventRecord) error {
	if r.err != nil {
		return r.err
	}
	record.Payload = bytes.Clone(record.Payload)
	r.records = append(r.records, record)
	return nil
}

func (r *fakeAuditRepository) ListByIncident(context.Context, domain.IncidentID) ([]application.AuditEventRecord, error) {
	return nil, nil
}
