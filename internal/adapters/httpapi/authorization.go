package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"github.com/araihu/xisnove/application"
)

type operationCredentialClass uint8

const (
	operationAnonymous operationCredentialClass = iota + 1
	operationHuman
	operationAgent
)

type operationAuthorizationMetadata struct {
	operationID string
	class       operationCredentialClass
	scope       application.Scope
	allowAdmin  bool
	allowToken  bool
}

type operationAuthorization struct {
	router     routers.Router
	operations map[string]operationAuthorizationMetadata
}

var anonymousOperationIDs = map[string]struct{}{
	"createSession": {}, "enrollAgent": {}, "getPublicStatusPage": {},
}

var agentOperationScopes = map[string]string{
	"heartbeatAgent": "agent:heartbeat", "leaseAgentWork": "agent:work",
	"uploadProbeResults": "agent:results", "upsertDiscoveryCandidates": "agent:discovery",
}

func newOperationAuthorization(spec *openapi3.T) (*operationAuthorization, error) {
	if spec == nil || spec.Paths == nil {
		return nil, errors.New("authorization metadata requires an OpenAPI document")
	}
	// Host matching is not an authorization boundary. The validator independently
	// validates the same host-neutral contract before this middleware runs.
	spec.Servers = nil
	router, err := gorillamux.NewRouter(spec)
	if err != nil {
		return nil, fmt.Errorf("build authorization router: %w", err)
	}
	metadata := make(map[string]operationAuthorizationMetadata)
	canonicalIDs := make(map[string]bool)
	seenAnonymous := make(map[string]bool, len(anonymousOperationIDs))
	seenAgent := make(map[string]bool, len(agentOperationScopes))
	for _, path := range spec.Paths.InMatchingOrder() {
		item := spec.Paths.Value(path)
		for _, operation := range item.Operations() {
			if operation == nil || operation.OperationID == "" {
				return nil, fmt.Errorf("operation at %s has no operationId", path)
			}
			routeOperationID := operation.OperationID
			id := canonicalOperationID(routeOperationID)
			if _, duplicate := metadata[routeOperationID]; duplicate {
				return nil, fmt.Errorf("duplicate operationId %q", routeOperationID)
			}
			if canonicalIDs[id] {
				return nil, fmt.Errorf("multiple generated operations map to operationId %q", id)
			}
			canonicalIDs[id] = true
			scopes, scopesOK := authorizationStringExtension(operation.Extensions["x-xisnove-scopes"])
			switch {
			case hasOperationID(anonymousOperationIDs, id):
				if operation.Security == nil || len(*operation.Security) != 0 || !scopesOK || len(scopes) != 0 {
					return nil, fmt.Errorf("anonymous operation %q has ambiguous security metadata", id)
				}
				metadata[routeOperationID] = operationAuthorizationMetadata{operationID: id, class: operationAnonymous}
				seenAnonymous[id] = true
			case hasAgentOperationID(id):
				expectedScope := agentOperationScopes[id]
				if operation.Security == nil || len(*operation.Security) == 0 || !scopesOK || len(scopes) != 1 || scopes[0] != expectedScope {
					return nil, fmt.Errorf("agent operation %q has ambiguous security metadata", id)
				}
				if !agentSecurityClass(*operation.Security) {
					return nil, fmt.Errorf("agent operation %q has unknown credential classes", id)
				}
				metadata[routeOperationID] = operationAuthorizationMetadata{operationID: id, class: operationAgent}
				seenAgent[id] = true
			default:
				if operation.Security == nil || len(*operation.Security) == 0 || !scopesOK || len(scopes) != 1 {
					return nil, fmt.Errorf("human operation %q has ambiguous security metadata", id)
				}
				normalized, err := application.NormalizeScopes([]application.Scope{application.Scope(scopes[0])})
				if err != nil {
					return nil, fmt.Errorf("human operation %q declares unknown scope: %w", id, err)
				}
				if err := application.Authorize(id, application.Principal{Kind: application.PrincipalAdmin}); err != nil {
					return nil, fmt.Errorf("human operation %q is absent from the authorization map: %w", id, err)
				}
				if err := application.Authorize(id, application.Principal{
					Kind: application.PrincipalAPIToken, Scopes: []application.Scope{normalized[0]},
				}); err != nil {
					return nil, fmt.Errorf("human operation %q scope disagrees with the authorization map: %w", id, err)
				}
				allowAdmin, allowToken, securityOK := humanSecurityClasses(*operation.Security)
				if !securityOK || (!allowAdmin && !allowToken) {
					return nil, fmt.Errorf("human operation %q has unknown credential classes", id)
				}
				metadata[routeOperationID] = operationAuthorizationMetadata{
					operationID: id, class: operationHuman, scope: normalized[0],
					allowAdmin: allowAdmin, allowToken: allowToken,
				}
			}
		}
	}
	for id := range anonymousOperationIDs {
		if !seenAnonymous[id] {
			return nil, fmt.Errorf("anonymous operation %q is absent", id)
		}
	}
	for id := range agentOperationScopes {
		if !seenAgent[id] {
			return nil, fmt.Errorf("agent operation %q is absent", id)
		}
	}
	return &operationAuthorization{router: router, operations: metadata}, nil
}

func (a *operationAuthorization) middleware(
	authenticateHuman BearerAuthenticator,
	authenticateAgent BearerAuthenticator,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			route, _, err := a.router.FindRoute(r)
			if err != nil || route == nil || route.Operation == nil {
				writeForbidden(w, r)
				return
			}
			metadata, ok := a.operations[route.Operation.OperationID]
			if !ok || metadata.operationID == "" {
				writeForbidden(w, r)
				return
			}
			if metadata.class == operationAnonymous {
				next.ServeHTTP(w, r)
				return
			}
			rawToken, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok {
				writeUnauthorized(w, r)
				return
			}
			authenticate := authenticateHuman
			if metadata.class == operationAgent {
				authenticate = authenticateAgent
			}
			if authenticate == nil {
				writeProblem(w, ToProblem(errors.New("credential authenticator is not configured"), correlationID(r)))
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
			if metadata.class == operationAgent {
				if principal.Kind != application.PrincipalAgent {
					writeForbidden(w, r)
					return
				}
			} else {
				if principal.Kind == application.PrincipalAdmin && !metadata.allowAdmin ||
					principal.Kind == application.PrincipalAPIToken && !metadata.allowToken {
					writeForbidden(w, r)
					return
				}
				if err := application.Authorize(metadata.operationID, principal); err != nil {
					writeForbidden(w, r)
					return
				}
			}
			ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeForbidden(w http.ResponseWriter, r *http.Request) {
	writeProblem(w, Problem{
		Type: "https://xisnove.dev/problems/insufficient-scope", Title: "Insufficient scope",
		Status: http.StatusForbidden, Code: "insufficient_scope", CorrelationId: correlationID(r),
	})
}

func authorizationStringExtension(value any) ([]string, bool) {
	raw, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}
		result = append(result, text)
	}
	return result, true
}

func hasOperationID(set map[string]struct{}, id string) bool {
	_, ok := set[id]
	return ok
}

func hasAgentOperationID(id string) bool {
	_, ok := agentOperationScopes[id]
	return ok
}

func humanSecurityClasses(requirements openapi3.SecurityRequirements) (bool, bool, bool) {
	var allowAdmin, allowToken bool
	for _, requirement := range requirements {
		if len(requirement) != 1 {
			return false, false, false
		}
		for name := range requirement {
			switch name {
			case "adminBearer":
				if allowAdmin {
					return false, false, false
				}
				allowAdmin = true
			case "apiTokenBearer":
				if allowToken {
					return false, false, false
				}
				allowToken = true
			default:
				return false, false, false
			}
		}
	}
	return allowAdmin, allowToken, true
}

func agentSecurityClass(requirements openapi3.SecurityRequirements) bool {
	if len(requirements) != 1 || len(requirements[0]) != 1 {
		return false
	}
	_, ok := requirements[0]["agentBearer"]
	return ok
}

func canonicalOperationID(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToLower(value[:1]) + value[1:]
}
