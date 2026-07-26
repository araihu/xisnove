package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/internal/adapters/httpapi"
)

func TestBearerAuthAddsAuthenticatedPrincipalToContext(t *testing.T) {
	authenticator := func(_ context.Context, rawToken string) (application.Principal, error) {
		if rawToken != "session-token" {
			return application.Principal{}, application.ErrInvalidCredentials
		}
		return application.Principal{
			Kind: application.PrincipalAdmin, SubjectID: "admin-1",
		}, nil
	}
	handler := httpapi.BearerAuth(authenticator)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			principal, ok := httpapi.PrincipalFromContext(r.Context())
			if !ok {
				t.Fatal("principal missing from context")
			}
			if principal.Kind != application.PrincipalAdmin || principal.SubjectID != "admin-1" {
				t.Fatalf("Principal = %#v", principal)
			}
			w.WriteHeader(http.StatusNoContent)
		},
	))

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer session-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
}

func TestBearerAuthRejectsMissingAuthorization(t *testing.T) {
	handler := httpapi.BearerAuth(func(
		context.Context,
		string,
	) (application.Principal, error) {
		return application.Principal{}, errors.New("must not authenticate")
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler was called")
	}))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
	if response.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
	}
}

func TestBearerAuthDoesNotClassifyInfrastructureFailureAsInvalidCredential(t *testing.T) {
	handler := httpapi.BearerAuth(func(
		context.Context,
		string,
	) (application.Principal, error) {
		return application.Principal{}, errors.New("store unavailable")
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler was called")
	}))

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer credential")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
}
