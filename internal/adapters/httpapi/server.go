package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/google/uuid"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/internal/adapters/observability"
)

var _ StrictServerInterface = (*Server)(nil)

type HandlerConfig struct {
	Server    *Server
	Ready     func(context.Context) error
	Metrics   http.Handler
	AdmitWork func(context.Context) (context.Context, func(), error)
}

func NewHandler(config HandlerConfig) (http.Handler, error) {
	spec, err := GetSwagger()
	if err != nil {
		return nil, err
	}
	authorization, err := newOperationAuthorization(spec)
	if err != nil {
		return nil, err
	}
	strict := NewStrictHandlerWithOptions(
		config.Server,
		nil,
		StrictHTTPServerOptions{
			RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, _ error) {
				writeProblem(w, ToProblem(&application.ValidationError{
					Fields: map[string]string{"request": "does not match the API contract"},
				}, correlationID(r)))
			},
			ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
				writeProblem(w, ToProblem(err, correlationID(r)))
			},
		},
	)
	api := HandlerWithOptions(strict, StdHTTPServerOptions{BaseRouter: NewOperatorActionServeMux()})
	api = admitAgentWork(config.AdmitWork)(api)
	var authenticateHuman, authenticateAgent BearerAuthenticator
	if config.Server != nil && config.Server.auth != nil {
		authenticateHuman = config.Server.auth.AuthenticateBearer
	}
	if config.Server != nil && config.Server.agents != nil {
		authenticateAgent = config.Server.agents.Authenticate
	}
	api = authorization.middleware(authenticateHuman, authenticateAgent)(api)
	api = nethttpmiddleware.OapiRequestValidatorWithOptions(
		spec,
		&nethttpmiddleware.Options{
			DoNotValidateServers: true,
			Options: openapi3filter.Options{
				AuthenticationFunc: func(
					context.Context,
					*openapi3filter.AuthenticationInput,
				) error {
					// Credential verification is operation-aware and occurs in
					// authenticateOperations immediately after contract validation.
					return nil
				},
			},
			ErrorHandler: func(w http.ResponseWriter, _ string, status int) {
				writeProblem(w, sanitizedValidationProblem(status, "unknown"))
			},
		},
	)(api)

	root := http.NewServeMux()
	root.Handle("/v1/", api)
	if config.Metrics != nil {
		root.Handle("GET /metrics", config.Metrics)
	}
	root.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	root.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if config.Ready == nil || config.Ready(r.Context()) != nil {
			writeProblem(w, Problem{
				Type: "https://xisnove.dev/problems/not-ready", Title: "Not ready",
				Status: http.StatusServiceUnavailable, Code: "not_ready",
				CorrelationId: correlationID(r),
			})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return correlationIDs(logRequests(recoverPanics(root))), nil
}

// sanitizedValidationProblem intentionally omits validator diagnostics. They
// can contain attacker-controlled request fragments, including credentials.
// A stable RFC 9457 envelope is sufficient for callers and remains bounded.
func sanitizedValidationProblem(status int, correlation string) Problem {
	if status < http.StatusBadRequest || status > 599 {
		status = http.StatusBadRequest
	}
	if correlation == "" {
		correlation = "unknown"
	}
	return Problem{
		Type: "https://xisnove.dev/problems/validation", Title: "Request validation failed",
		Status: int32(status), Code: "validation_failed", CorrelationId: correlation,
	}
}

// operatorActionServeMux adapts an OpenAPI action suffix to net/http's
// wildcard grammar without changing the canonical public route. oapi-codegen
// emits `{generation}:revoke`, which ServeMux rejects; the wrapper restores the
// generation value expected by the generated parser before invoking it.
type operatorActionServeMux struct{ *http.ServeMux }

// NewOperatorActionServeMux returns the safe generated-server router for
// callers that compose a strict handler outside NewHandler.
func NewOperatorActionServeMux() ServeMux {
	return &operatorActionServeMux{ServeMux: http.NewServeMux()}
}

func (m *operatorActionServeMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	const action = "{generation}:revoke"
	if !strings.Contains(pattern, action) {
		m.ServeMux.HandleFunc(pattern, handler)
		return
	}
	pattern = strings.Replace(pattern, action, "{generation}", 1)
	m.ServeMux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		generation := r.PathValue("generation")
		if !strings.HasSuffix(generation, ":revoke") {
			http.NotFound(w, r)
			return
		}
		r.SetPathValue("generation", strings.TrimSuffix(generation, ":revoke"))
		handler(w, r)
	})
}

func admitAgentWork(
	admit func(context.Context) (context.Context, func(), error),
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if admit == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/v1/agent/work:lease" {
				next.ServeHTTP(w, r)
				return
			}
			ctx, release, err := admit(r.Context())
			if err != nil {
				writeProblem(w, Problem{
					Type: "https://xisnove.dev/problems/not-ready", Title: "Not ready",
					Status: http.StatusServiceUnavailable, Code: "not_ready",
					CorrelationId: correlationID(r),
				})
				return
			}
			defer release()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func correlationIDs(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = uuid.NewString()
		}
		r.Header.Set("X-Request-ID", id)
		w.Header().Set("X-Request-ID", id)
		ctx := observability.ContextWithIDs(r.Context(), observability.IDs{Correlation: id})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		next.ServeHTTP(w, r)
		slog.InfoContext(r.Context(), "HTTP request",
			"method", r.Method,
			"path", r.URL.Path,
			"duration", time.Since(startedAt),
		)
	})
}

func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.ErrorContext(r.Context(), "panic serving request",
					"panic", recovered, "stack", string(debug.Stack()),
				)
				writeProblem(w, ToProblem(
					context.Canceled,
					correlationID(r),
				))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func correlationID(r *http.Request) string {
	id := r.Header.Get("X-Request-ID")
	if id == "" {
		return "unknown"
	}
	return id
}

func writeProblem(w http.ResponseWriter, problem Problem) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(statusForProblem(problem))
	_ = json.NewEncoder(w).Encode(problem)
}
