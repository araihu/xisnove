package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/araihu/xisnove/application"
)

func TestAuthorizationAllowsAdministratorSession(t *testing.T) {
	authorization := testOperationAuthorization(t)
	called := false
	handler := authorization.middleware(testHumanAuthenticator, testAgentAuthenticator)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			called = true
			principal, ok := PrincipalFromContext(r.Context())
			if !ok || principal.Kind != application.PrincipalAdmin {
				t.Fatalf("principal = %#v, %v", principal, ok)
			}
			w.WriteHeader(http.StatusNoContent)
		},
	))
	response := authorizeRequest(handler, http.MethodGet, "/v1/monitors", "admin-session")
	if !called || response.Code != http.StatusNoContent {
		t.Fatalf("called = %v, status = %d, body = %s", called, response.Code, response.Body)
	}
}

func TestAuthorizationAllowsOnlyDeclaredTokenScope(t *testing.T) {
	authorization := testOperationAuthorization(t)
	handler := authorization.middleware(testHumanAuthenticator, testAgentAuthenticator)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
	))
	allowed := authorizeRequest(handler, http.MethodGet, "/v1/monitors", "monitor-reader")
	if allowed.Code != http.StatusNoContent {
		t.Fatalf("allowed status = %d, body = %s", allowed.Code, allowed.Body)
	}
	denied := authorizeRequest(handler, http.MethodPost, "/v1/monitors", "monitor-reader")
	if denied.Code != http.StatusForbidden || !strings.Contains(denied.Body.String(), `"code":"insufficient_scope"`) {
		t.Fatalf("denied status = %d, body = %s", denied.Code, denied.Body)
	}
	adminOnly := authorizeRequest(handler, http.MethodPost, "/v1/api-tokens", "token-writer")
	if adminOnly.Code != http.StatusForbidden {
		t.Fatalf("API-token credential reached admin-session-only operation: %d %s", adminOnly.Code, adminOnly.Body)
	}
}

func TestAuthorizationMetadataRejectsMissingMultipleAndUnknownScopes(t *testing.T) {
	for name, mutate := range map[string]func(*operationAuthorizationMetadataFixture){
		"missing": func(fixture *operationAuthorizationMetadataFixture) {
			delete(fixture.operation.Extensions, "x-xisnove-scopes")
		},
		"multiple": func(fixture *operationAuthorizationMetadataFixture) {
			fixture.operation.Extensions["x-xisnove-scopes"] = []any{"monitors:read", "monitors:write"}
		},
		"unknown": func(fixture *operationAuthorizationMetadataFixture) {
			fixture.operation.Extensions["x-xisnove-scopes"] = []any{"root:all"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			spec, err := GetSwagger()
			if err != nil {
				t.Fatal(err)
			}
			fixture := operationAuthorizationFixture(t, spec, "listMonitors")
			mutate(&fixture)
			if _, err := newOperationAuthorization(spec); err == nil {
				t.Fatal("newOperationAuthorization() accepted ambiguous metadata")
			}
		})
	}
}

func TestAuthorizationReturns401ForInvalidCredentialAnd403ForMissingScope(t *testing.T) {
	authorization := testOperationAuthorization(t)
	handler := authorization.middleware(testHumanAuthenticator, testAgentAuthenticator)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
	))
	invalid := authorizeRequest(handler, http.MethodGet, "/v1/monitors", "invalid")
	if invalid.Code != http.StatusUnauthorized || invalid.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("invalid response = %d %#v", invalid.Code, invalid.Header())
	}
	missing := authorizeRequest(handler, http.MethodGet, "/v1/monitors", "token-reader")
	if missing.Code != http.StatusForbidden {
		t.Fatalf("missing scope response = %d %s", missing.Code, missing.Body)
	}
}

func TestAuthorizationDeniesUnknownOperation(t *testing.T) {
	authorization := testOperationAuthorization(t)
	called := false
	handler := authorization.middleware(testHumanAuthenticator, testAgentAuthenticator)(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) { called = true },
	))
	response := authorizeRequest(handler, http.MethodGet, "/v1/not-in-contract", "admin-session")
	if called || response.Code != http.StatusForbidden {
		t.Fatalf("called = %v, response = %d %s", called, response.Code, response.Body)
	}
}

func TestAgentCredentialCannotCallManagementOperation(t *testing.T) {
	authorization := testOperationAuthorization(t)
	handler := authorization.middleware(testHumanAuthenticator, testAgentAuthenticator)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
	))
	management := authorizeRequest(handler, http.MethodGet, "/v1/monitors", "agent-credential")
	if management.Code != http.StatusUnauthorized {
		t.Fatalf("management response = %d %s", management.Code, management.Body)
	}
	agent := authorizeRequest(handler, http.MethodPost, "/v1/agent/heartbeat", "agent-credential")
	if agent.Code != http.StatusNoContent {
		t.Fatalf("agent response = %d %s", agent.Code, agent.Body)
	}
}

func TestAnonymousStatusAndEnrollmentBypassBearer(t *testing.T) {
	authorization := testOperationAuthorization(t)
	authenticate := func(context.Context, string) (application.Principal, error) {
		t.Fatal("anonymous operation attempted authentication")
		return application.Principal{}, errors.New("unreachable")
	}
	handler := authorization.middleware(authenticate, authenticate)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
	))
	for _, request := range []struct{ method, path string }{
		{http.MethodPost, "/v1/sessions"},
		{http.MethodPost, "/v1/agent-enrollments"},
		{http.MethodGet, "/v1/status-page"},
	} {
		response := authorizeRequest(handler, request.method, request.path, "")
		if response.Code != http.StatusNoContent {
			t.Errorf("%s %s = %d %s", request.method, request.path, response.Code, response.Body)
		}
	}
}

func testOperationAuthorization(t *testing.T) *operationAuthorization {
	t.Helper()
	spec, err := GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := newOperationAuthorization(spec)
	if err != nil {
		t.Fatal(err)
	}
	return authorization
}

func testHumanAuthenticator(_ context.Context, raw string) (application.Principal, error) {
	switch raw {
	case "admin-session":
		return application.Principal{Kind: application.PrincipalAdmin}, nil
	case "monitor-reader":
		return application.Principal{Kind: application.PrincipalAPIToken, Scopes: []application.Scope{application.ScopeMonitorsRead}}, nil
	case "token-reader":
		return application.Principal{Kind: application.PrincipalAPIToken, Scopes: []application.Scope{application.ScopeTokensRead}}, nil
	case "token-writer":
		return application.Principal{Kind: application.PrincipalAPIToken, Scopes: []application.Scope{application.ScopeTokensWrite}}, nil
	default:
		return application.Principal{}, application.ErrInvalidCredentials
	}
}

type operationAuthorizationMetadataFixture struct {
	operation *openapi3.Operation
}

func operationAuthorizationFixture(t *testing.T, spec *openapi3.T, operationID string) operationAuthorizationMetadataFixture {
	t.Helper()
	for _, path := range spec.Paths.InMatchingOrder() {
		for _, operation := range spec.Paths.Value(path).Operations() {
			if operation != nil && canonicalOperationID(operation.OperationID) == operationID {
				return operationAuthorizationMetadataFixture{operation: operation}
			}
		}
	}
	t.Fatalf("operation %q not found", operationID)
	return operationAuthorizationMetadataFixture{}
}

func testAgentAuthenticator(_ context.Context, raw string) (application.Principal, error) {
	if raw != "agent-credential" {
		return application.Principal{}, application.ErrInvalidCredentials
	}
	return application.Principal{Kind: application.PrincipalAgent}, nil
}

func authorizeRequest(handler http.Handler, method, path, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
