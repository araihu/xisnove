package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/araihu/xisnove/application"
)

type BearerAuthenticator func(
	context.Context,
	string,
) (application.Principal, error)

type principalContextKey struct{}

func BearerAuth(authenticate BearerAuthenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawToken, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok {
				writeUnauthorized(w, r)
				return
			}
			principal, err := authenticate(r.Context(), rawToken)
			if err != nil {
				if isAuthenticationError(err) {
					writeUnauthorized(w, r)
				} else {
					writeProblem(w, ToProblem(err, correlationID(r)))
				}
				return
			}
			ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func PrincipalFromContext(ctx context.Context) (application.Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(application.Principal)
	return principal, ok
}

func ContextWithPrincipal(
	ctx context.Context,
	principal application.Principal,
) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func bearerToken(header string) (string, bool) {
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || fields[1] == "" {
		return "", false
	}
	return fields[1], true
}

func writeUnauthorized(w http.ResponseWriter, r *http.Request) {
	correlationID := r.Header.Get("X-Request-ID")
	if correlationID == "" {
		correlationID = "unknown"
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("WWW-Authenticate", "Bearer")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(Problem{
		Type:          "https://xisnove.dev/problems/unauthorized",
		Title:         "Authentication required",
		Status:        http.StatusUnauthorized,
		Code:          "unauthorized",
		CorrelationId: correlationID,
	})
}

func (s *Server) CreateSession(
	ctx context.Context,
	request CreateSessionRequestObject,
) (CreateSessionResponseObject, error) {
	if request.Body == nil || request.Body.Password == nil {
		problem, status, _ := problemFromError(&application.ValidationError{
			Fields: map[string]string{"body": "is required"},
		})
		return CreateSessiondefaultApplicationProblemPlusJSONResponse{
			Body: problem, StatusCode: status,
		}, nil
	}
	session, err := s.auth.CreateSession(
		ctx,
		string(request.Body.Email),
		*request.Body.Password,
	)
	if err != nil {
		problem, status, mapped := problemFromError(err)
		if mapped {
			return CreateSessiondefaultApplicationProblemPlusJSONResponse{
				Body: problem, StatusCode: status,
			}, nil
		}
		return nil, err
	}
	return CreateSession201JSONResponse{
		Token: session.Token, ExpiresAt: session.ExpiresAt,
	}, nil
}

func (s *Server) RevokeCurrentSession(
	ctx context.Context,
	_ RevokeCurrentSessionRequestObject,
) (RevokeCurrentSessionResponseObject, error) {
	principal, ok := PrincipalFromContext(ctx)
	if !ok {
		return RevokeCurrentSessiondefaultApplicationProblemPlusJSONResponse{
			Body: Problem{
				Type: "https://xisnove.dev/problems/unauthorized", Title: "Authentication required",
				Status: http.StatusUnauthorized, Code: "unauthorized", CorrelationId: "unknown",
			},
			StatusCode: http.StatusUnauthorized,
		}, nil
	}
	if err := s.auth.RevokeCurrentSession(ctx, principal); err != nil {
		problem, status, mapped := problemFromError(err)
		if mapped {
			return RevokeCurrentSessiondefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
		}
		return nil, err
	}
	return RevokeCurrentSession204Response{}, nil
}
