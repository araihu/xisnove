package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/a-h/templ"
	"github.com/araihu/goshtoso/assets"
	"github.com/araihu/xisnove/ui/internal/controlplane"
	"github.com/araihu/xisnove/ui/internal/security"
	"github.com/araihu/xisnove/ui/internal/view"
)

const maxFormBytes = 64 << 10

type Config struct {
	ControlPlane   controlplane.Client
	CookieSecret   []byte
	CookieSecure   bool
	RequestTimeout time.Duration
	Random         io.Reader
	Logger         *slog.Logger
}

type server struct {
	controlPlane controlplane.Client
	cookies      *security.CookieManager
	timeout      time.Duration
	random       io.Reader
	logger       *slog.Logger
	fallbackID   atomic.Uint64
}

func New(cfg Config) (http.Handler, error) {
	if cfg.ControlPlane == nil {
		return nil, errors.New("control-plane client is required")
	}
	if cfg.RequestTimeout <= 0 {
		return nil, errors.New("request timeout must be positive")
	}
	random := cfg.Random
	if random == nil {
		random = rand.Reader
	}
	cookies, err := security.NewCookieManager(cfg.CookieSecret, cfg.CookieSecure, random)
	if err != nil {
		return nil, fmt.Errorf("configure cookies: %w", err)
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s := &server{
		controlPlane: cfg.ControlPlane,
		cookies:      cookies,
		timeout:      cfg.RequestTimeout,
		random:       random,
		logger:       logger,
	}

	mux := http.NewServeMux()
	mux.Handle("/assets/", assets.Handler())
	mux.HandleFunc("/", s.route)

	var handler http.Handler = mux
	handler = s.recoverPanics(handler)
	handler = s.securityHeaders(handler)
	handler = s.accessLog(handler)
	handler = s.requestTimeout(handler)
	handler = s.correlation(handler)
	return handler, nil
}

func (s *server) route(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.URL.Path {
	case "/":
		if r.Method != http.MethodGet {
			s.methodNotAllowed(w, r, http.MethodGet)
			return
		}
		s.dashboard(w, r)
	case "/login":
		s.login(w, r)
	case "/logout":
		if r.Method != http.MethodPost {
			s.methodNotAllowed(w, r, http.MethodPost)
			return
		}
		s.logout(w, r)
	case "/status":
		if r.Method != http.MethodGet {
			s.methodNotAllowed(w, r, http.MethodGet)
			return
		}
		s.publicStatus(w, r)
	default:
		s.writeProblem(w, r, problem{
			Type:   "urn:xisnove:ui:problem:not-found",
			Title:  "Page not found",
			Status: http.StatusNotFound,
			Detail: "The requested UI page does not exist.",
			Code:   "not_found",
		})
	}
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if _, ok := s.cookies.Session(r); ok {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		token, err := s.cookies.IssueLoginCSRF(w)
		if err != nil {
			s.internalProblem(w, r)
			return
		}
		s.renderAdaptive(w, r, http.StatusOK, view.LoginPage(token, ""), view.LoginContent(token, ""))
	case http.MethodPost:
		s.loginPost(w, r)
	default:
		s.methodNotAllowed(w, r, http.MethodGet, http.MethodPost)
	}
}

func (s *server) loginPost(w http.ResponseWriter, r *http.Request) {
	if err := parseForm(w, r); err != nil {
		s.writeProblem(w, r, invalidRequestProblem())
		return
	}
	if !s.cookies.ValidateLoginCSRF(r, r.PostForm.Get("_csrf")) {
		s.writeProblem(w, r, csrfProblem())
		return
	}
	credential, err := s.controlPlane.ExchangeAdministratorCredentials(
		r.Context(),
		r.PostForm.Get("username"),
		r.PostForm.Get("password"),
	)
	if err != nil {
		s.loginError(w, r, err)
		return
	}
	if credential == "" {
		s.internalProblem(w, r)
		return
	}
	s.cookies.SetSession(w, credential)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) loginError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		s.writeProblem(w, r, gatewayTimeoutProblem())
		return
	}
	if errors.Is(err, context.Canceled) {
		s.writeProblem(w, r, requestCanceledProblem())
		return
	}
	if errors.Is(err, controlplane.ErrInvalidCredentials) {
		if acceptsProblemJSON(r) {
			s.writeProblem(w, r, invalidCredentialsProblem())
			return
		}
		token, issueErr := s.cookies.IssueLoginCSRF(w)
		if issueErr != nil {
			s.internalProblem(w, r)
			return
		}
		s.renderAdaptive(w, r, http.StatusUnauthorized,
			view.LoginPage(token, "The username or password was not accepted."),
			view.LoginContent(token, "The username or password was not accepted."),
		)
		return
	}
	s.writeProblem(w, r, upstreamProblem())
}

func (s *server) dashboard(w http.ResponseWriter, r *http.Request) {
	credential, ok := s.cookies.Session(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	csrfToken := s.cookies.SessionCSRF(credential)
	s.renderAdaptive(w, r, http.StatusOK, view.ShellPage(csrfToken), view.DashboardContent())
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	credential, ok := s.cookies.Session(r)
	if !ok {
		s.writeProblem(w, r, unauthorizedProblem())
		return
	}
	if err := parseForm(w, r); err != nil {
		s.writeProblem(w, r, invalidRequestProblem())
		return
	}
	if !s.cookies.ValidateSessionCSRF(credential, r.PostForm.Get("_csrf")) {
		s.writeProblem(w, r, csrfProblem())
		return
	}
	if err := s.controlPlane.RevokeSession(r.Context(), credential); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			s.writeProblem(w, r, gatewayTimeoutProblem())
			return
		}
		if errors.Is(err, controlplane.ErrUnauthorized) {
			s.cookies.ClearSession(w)
			s.writeProblem(w, r, unauthorizedProblem())
			return
		}
		s.writeProblem(w, r, upstreamProblem())
		return
	}
	s.cookies.ClearSession(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *server) publicStatus(w http.ResponseWriter, r *http.Request) {
	s.renderAdaptive(w, r, http.StatusOK, view.StatusPage(), view.StatusContent())
}

func (s *server) methodNotAllowed(w http.ResponseWriter, r *http.Request, allowed ...string) {
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	s.writeProblem(w, r, problem{
		Type:   "urn:xisnove:ui:problem:method-not-allowed",
		Title:  "Method not allowed",
		Status: http.StatusMethodNotAllowed,
		Detail: "This UI route does not accept the requested HTTP method.",
		Code:   "method_not_allowed",
	})
}

func (s *server) renderAdaptive(w http.ResponseWriter, r *http.Request, status int, full, fragment templ.Component) {
	w.Header().Add("Vary", "HX-Request")
	component := full
	if isHTMX(r) {
		component = fragment
	}
	if err := renderComponent(r.Context(), w, status, component); err != nil {
		s.logger.ErrorContext(r.Context(), "render response failed", "correlation_id", correlationID(r.Context()))
	}
}

func renderComponent(ctx context.Context, w http.ResponseWriter, status int, component templ.Component) error {
	var body bytes.Buffer
	if err := component.Render(ctx, &body); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, err := w.Write(body.Bytes())
	return err
}

func parseForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	return r.ParseForm()
}

func isHTMX(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("HX-Request")), "true")
}

type problem struct {
	Type          string `json:"type"`
	Title         string `json:"title"`
	Status        int    `json:"status"`
	Detail        string `json:"detail"`
	Instance      string `json:"instance"`
	Code          string `json:"code"`
	CorrelationID string `json:"correlation_id"`
}

func (s *server) writeProblem(w http.ResponseWriter, r *http.Request, p problem) {
	p.Instance = r.URL.Path
	p.CorrelationID = correlationID(r.Context())
	if acceptsProblemJSON(r) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(p.Status)
		if err := json.NewEncoder(w).Encode(p); err != nil {
			s.logger.ErrorContext(r.Context(), "encode problem failed", "correlation_id", p.CorrelationID)
		}
		return
	}
	v := view.Problem{Title: p.Title, Detail: p.Detail, Code: p.Code, CorrelationID: p.CorrelationID}
	s.renderAdaptive(w, r, p.Status, view.ProblemPage(v), view.ProblemContent(v))
}

func acceptsProblemJSON(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/problem+json")
}

func (s *server) internalProblem(w http.ResponseWriter, r *http.Request) {
	s.writeProblem(w, r, problem{
		Type:   "urn:xisnove:ui:problem:internal",
		Title:  "Unexpected UI error",
		Status: http.StatusInternalServerError,
		Detail: "The UI could not complete the request. Use the correlation ID when checking server logs.",
		Code:   "internal_error",
	})
}

func invalidRequestProblem() problem {
	return problem{Type: "urn:xisnove:ui:problem:invalid-request", Title: "Invalid request", Status: http.StatusBadRequest, Detail: "The submitted form could not be read.", Code: "invalid_request"}
}

func csrfProblem() problem {
	return problem{Type: "urn:xisnove:ui:problem:csrf", Title: "Request verification failed", Status: http.StatusForbidden, Detail: "Refresh the page and try the action again.", Code: "csrf_failed"}
}

func invalidCredentialsProblem() problem {
	return problem{Type: "urn:xisnove:ui:problem:invalid-credentials", Title: "Sign-in failed", Status: http.StatusUnauthorized, Detail: "The username or password was not accepted.", Code: "invalid_credentials"}
}

func unauthorizedProblem() problem {
	return problem{Type: "urn:xisnove:ui:problem:unauthorized", Title: "Sign-in required", Status: http.StatusUnauthorized, Detail: "Sign in again before continuing.", Code: "unauthorized"}
}

func gatewayTimeoutProblem() problem {
	return problem{Type: "urn:xisnove:ui:problem:gateway-timeout", Title: "Control plane timed out", Status: http.StatusGatewayTimeout, Detail: "The control plane did not answer before the UI timeout.", Code: "gateway_timeout"}
}

func requestCanceledProblem() problem {
	return problem{Type: "urn:xisnove:ui:problem:request-canceled", Title: "Request canceled", Status: http.StatusRequestTimeout, Detail: "The browser canceled the request before it completed.", Code: "request_canceled"}
}

func upstreamProblem() problem {
	return problem{Type: "urn:xisnove:ui:problem:control-plane-unavailable", Title: "Control plane unavailable", Status: http.StatusBadGateway, Detail: "The UI could not complete the control-plane request.", Code: "control_plane_unavailable"}
}

type contextKey string

const correlationKey contextKey = "correlation-id"

func (s *server) correlation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validCorrelationID(id) {
			id = s.newCorrelationID()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), correlationKey, id)))
	})
}

func (s *server) newCorrelationID() string {
	value := make([]byte, 16)
	if _, err := io.ReadFull(s.random, value); err == nil {
		return base64.RawURLEncoding.EncodeToString(value)
	}
	return fmt.Sprintf("ui-%d", s.fallbackID.Add(1))
}

func validCorrelationID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, ch := range value {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || strings.ContainsRune("-_.:/", ch)) {
			return false
		}
	}
	return true
}

func correlationID(ctx context.Context) string {
	id, _ := ctx.Value(correlationKey).(string)
	return id
}

func (s *server) requestTimeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.logger.InfoContext(r.Context(), "http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"duration_ms", time.Since(started).Milliseconds(),
			"correlation_id", correlationID(r.Context()),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusRecorder) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (s *server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				s.logger.ErrorContext(r.Context(), "panic recovered", "correlation_id", correlationID(r.Context()))
				s.internalProblem(w, r)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
