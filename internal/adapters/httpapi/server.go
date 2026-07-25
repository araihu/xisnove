package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
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
	api := Handler(strict)
	api = admitAgentWork(config.AdmitWork)(api)
	api = authenticateOperations(config.Server)(api)
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
			ErrorHandler: func(w http.ResponseWriter, message string, status int) {
				detail := message
				writeProblem(w, Problem{
					Type:  "https://xisnove.dev/problems/validation",
					Title: "Request validation failed", Status: int32(status),
					Code: "validation_failed", CorrelationId: "unknown",
					Detail: &detail,
				})
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

func authenticateOperations(server *Server) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost &&
				(r.URL.Path == "/v1/sessions" ||
					r.URL.Path == "/v1/agent-enrollments") {
				next.ServeHTTP(w, r)
				return
			}
			authenticate := server.auth.AuthenticateSession
			if r.URL.Path == "/v1/agent/heartbeat" ||
				r.URL.Path == "/v1/agent/results:batch" ||
				r.URL.Path == "/v1/agent/work:lease" {
				authenticate = server.agents.Authenticate
			}
			BearerAuth(authenticate)(next).ServeHTTP(w, r)
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
