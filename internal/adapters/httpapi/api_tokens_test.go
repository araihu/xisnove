package httpapi

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/application"
	appport "github.com/araihu/xisnove/application/port"
	internalcrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestCreateAPITokenReturnsCredentialAndListRedactsIt(t *testing.T) {
	server, principal := newHumanClientTestServer(t)
	ctx := ContextWithPrincipal(context.Background(), principal)
	expiresAt := time.Now().UTC().Add(time.Hour)
	createdResponse, err := server.CreateAPIToken(ctx, CreateAPITokenRequestObject{
		Body: &CreateAPITokenRequest{
			Name: "deploy", Scopes: []APITokenScope{MonitorsRead, StatusRead}, ExpiresAt: &expiresAt,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	created, ok := createdResponse.(CreateAPIToken201JSONResponse)
	if !ok || created.Token == "" || created.ApiToken.Name != "deploy" || len(created.ApiToken.Scopes) != 2 {
		t.Fatalf("created response = %#v", createdResponse)
	}

	listedResponse, err := server.ListAPITokens(ctx, ListAPITokensRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	listed, ok := listedResponse.(ListAPITokens200JSONResponse)
	if !ok || len(listed.Items) != 1 || listed.Items[0].Id != created.ApiToken.Id {
		t.Fatalf("listed response = %#v", listedResponse)
	}
}

func TestRevokeAPITokenRemovesServerCredential(t *testing.T) {
	server, principal := newHumanClientTestServer(t)
	ctx := ContextWithPrincipal(context.Background(), principal)
	createdResponse, err := server.CreateAPIToken(ctx, CreateAPITokenRequestObject{
		Body: &CreateAPITokenRequest{Name: "reader", Scopes: []APITokenScope{MonitorsRead}},
	})
	if err != nil {
		t.Fatal(err)
	}
	created := createdResponse.(CreateAPIToken201JSONResponse)
	if _, err := server.auth.AuthenticateBearer(ctx, created.Token); err != nil {
		t.Fatalf("authenticate new API token: %v", err)
	}
	revokedResponse, err := server.RevokeAPIToken(ctx, RevokeAPITokenRequestObject{TokenId: created.ApiToken.Id})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := revokedResponse.(RevokeAPIToken204Response); !ok {
		t.Fatalf("revoked response = %#v", revokedResponse)
	}
	if _, err := server.auth.AuthenticateBearer(ctx, created.Token); !errors.Is(err, application.ErrInvalidCredentials) {
		t.Fatalf("authenticate revoked API token: %v", err)
	}
}

func TestRevokeCurrentSessionRemovesServerState(t *testing.T) {
	server, principal := newHumanClientTestServer(t)
	ctx := ContextWithPrincipal(context.Background(), principal)
	response, err := server.RevokeCurrentSession(ctx, RevokeCurrentSessionRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := response.(RevokeCurrentSession204Response); !ok {
		t.Fatalf("response = %#v", response)
	}
	if _, err := server.auth.AuthenticateBearer(context.Background(), "session-credential"); !errors.Is(err, application.ErrInvalidCredentials) {
		t.Fatalf("authenticate revoked session: %v", err)
	}
}

func newHumanClientTestServer(t *testing.T) (*Server, application.Principal) {
	t.Helper()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "human-clients.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(db)
	now := time.Now().UTC()
	const adminID = "11111111-1111-4111-8111-111111111111"
	if err := store.Transact(ctx, func(ctx context.Context, repositories appport.Repositories) error {
		if err := repositories.Admins.Create(ctx, appport.AdminRecord{
			ID: adminID, Email: "admin@example.com", PasswordHash: "unused", CreatedAt: now,
		}); err != nil {
			return err
		}
		return repositories.Sessions.Create(ctx, appport.SessionRecord{
			ID: "22222222-2222-4222-8222-222222222222", AdminID: adminID,
			TokenHash: internalcrypto.NewProductionTokenIssuer().Hash("session-credential"), ExpiresAt: now.Add(time.Hour),
		})
	}); err != nil {
		t.Fatal(err)
	}
	tokens := internalcrypto.NewProductionTokenIssuer()
	auth := application.NewAuthService(application.AuthServiceConfig{
		Store: store, Tokens: tokens, Now: time.Now, NewID: uuid.NewString,
	})
	apiTokens := application.NewAPITokenService(application.APITokenServiceConfig{
		Store: store, Tokens: tokens, Now: time.Now, NewID: uuid.NewString,
	})
	principal, err := auth.AuthenticateBearer(ctx, "session-credential")
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(ServerConfig{Auth: auth, APITokens: apiTokens}), principal
}
