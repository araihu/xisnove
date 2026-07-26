package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	"github.com/araihu/xisnove/internal/adapters/ids"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestOperatorHandlerAppliesAgentAndReplaysWithoutCredential(t *testing.T) {
	ctx := context.Background()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "operator-http.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(db)
	tokens := xiscrypto.NewProductionTokenIssuer()
	now := time.Now().UTC()
	auth := application.NewAuthService(application.AuthServiceConfig{
		Store: store, Passwords: xiscrypto.NewProductionPasswordHasher(), Tokens: tokens,
		SessionDuration: time.Hour, Now: func() time.Time { return now }, NewID: ids.NewUUID,
	})
	if err := auth.BootstrapAdmin(ctx, "operator@example.com", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	session, err := auth.CreateSession(ctx, "operator@example.com", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.AuthenticateBearer(ctx, session.Token)
	if err != nil {
		t.Fatal(err)
	}
	apiTokens := application.NewAPITokenService(application.APITokenServiceConfig{
		Store: store, Tokens: tokens, Now: func() time.Time { return now }, NewID: ids.NewUUID,
	})
	operatorToken, err := apiTokens.Create(ctx, principal, application.CreateAPITokenCommand{
		Label: "operator", Scopes: []application.Scope{application.ScopeOperatorProvision},
	})
	if err != nil {
		t.Fatal(err)
	}
	configuration := application.NewConfigurationService(store, func() time.Time { return now }, ids.NewUUID)
	location, err := configuration.CreateLocation(ctx, application.CreateLocationCommand{Name: "edge"})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(HandlerConfig{
		Server: NewServer(ServerConfig{
			Auth: auth, APITokens: apiTokens, Configuration: configuration,
			Operator: application.OperatorService{Store: store, Credentials: tokens},
		}),
		Ready: func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	credential := "operator-credential-012345678901234567890123456789"
	body := map[string]any{
		"owner": map[string]string{"key": "monitoring.xisnove.io/Agent/default/edge", "uid": "agent-uid-1"},
		"name":  "edge", "locationId": location.ID, "enabled": true,
		"capabilities":      []string{"http"},
		"initialCredential": map[string]any{"generation": 1, "credential": credential},
	}
	first := performOperatorRequest(t, handler, http.MethodPost, "/v1/operator/agents:apply", "agent-apply-1", operatorToken.Token, body)
	if first.Code != http.StatusOK {
		t.Fatalf("apply status = %d, body = %s", first.Code, first.Body.String())
	}
	if strings.Contains(first.Body.String(), credential) {
		t.Fatalf("apply response leaked credential: %s", first.Body.String())
	}
	var applied OperatorAgentApplyResult
	if err := json.NewDecoder(first.Body).Decode(&applied); err != nil {
		t.Fatal(err)
	}
	if applied.ExternalId.String() == "" || applied.CredentialGeneration != 1 {
		t.Fatalf("apply result = %#v", applied)
	}
	replayed := performOperatorRequest(t, handler, http.MethodPost, "/v1/operator/agents:apply", "agent-apply-1", operatorToken.Token, body)
	if replayed.Code != http.StatusOK || !strings.Contains(replayed.Body.String(), applied.ExternalId.String()) || strings.Contains(replayed.Body.String(), credential) {
		t.Fatalf("lost-response replay = %d %s", replayed.Code, replayed.Body.String())
	}
}

func performOperatorRequest(t *testing.T, handler http.Handler, method, path, key, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
