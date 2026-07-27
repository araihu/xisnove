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
	"sync"
	"sync/atomic"
	"time"

	"github.com/a-h/templ"
	"github.com/araihu/goshtoso/assets"
	"github.com/araihu/xisnove/sdk"
	"github.com/araihu/xisnove/ui/internal/controlplane"
	"github.com/araihu/xisnove/ui/internal/security"
	"github.com/araihu/xisnove/ui/internal/view"
)

const (
	maxFormBytes      = 64 << 10
	monitorPageSize   = int32(25)
	maxSearchPageWalk = 4
)

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
	mux.Handle("GET /assets/", assets.Handler())
	mux.HandleFunc("GET /ui/app.js", serveApplicationJS)
	mux.HandleFunc("/", s.route)

	var handler http.Handler = mux
	handler = s.recoverPanics(handler)
	handler = s.securityHeaders(handler)
	handler = s.accessLog(handler)
	handler = s.requestTimeout(handler)
	handler = s.correlation(handler)
	return handler, nil
}

const applicationJS = `(() => {
  let pendingFocus = null;
  let refreshGeneration = 0;
  let refreshController = null;

  function focusMain() {
    const main = document.getElementById("main-content");
    if (!main) return;
    main.scrollTop = 0;
    const explicit = main.querySelector("[data-autofocus]");
    (explicit || main).focus({preventScroll: true});
    const heading = main.querySelector("h1");
    if (heading) document.title = heading.textContent.trim() + " · Xisnove";
  }

  function rememberFocus(event) {
    const source = event.detail?.elt;
    if (!source?.closest?.("[data-preserve-focus]")) return;
    const active = document.activeElement;
    if (!active?.id) return;
    pendingFocus = {
      id: active.id,
      start: typeof active.selectionStart === "number" ? active.selectionStart : null,
      end: typeof active.selectionEnd === "number" ? active.selectionEnd : null,
    };
  }

  function settle(event) {
    if (pendingFocus) {
      const target = document.getElementById(pendingFocus.id);
      if (target) {
        target.focus({preventScroll: true});
        if (pendingFocus.start !== null && target.setSelectionRange) {
          target.setSelectionRange(pendingFocus.start, pendingFocus.end);
        }
        pendingFocus = null;
        return;
      }
      pendingFocus = null;
    }
    if (event.detail?.elt?.closest?.("[data-preserve-focus]")) return;
    focusMain();
  }

  function invalidateAuthoritativeRefresh() {
    refreshGeneration += 1;
    refreshController?.abort();
    refreshController = null;
  }

  async function refreshAuthoritative() {
    const main = document.getElementById("main-content");
    if (!main || !location.pathname.startsWith("/monitors")) return focusMain();
    refreshController?.abort();
    const controller = new AbortController();
    const generation = ++refreshGeneration;
    const href = location.href;
    refreshController = controller;
    const ownsRefresh = () => refreshGeneration === generation && refreshController === controller && !controller.signal.aborted && location.href === href && main.isConnected && document.getElementById("main-content") === main;
    try {
      const response = await fetch(href, {headers: {"HX-Request": "true"}, cache: "no-store", signal: controller.signal});
      if (!ownsRefresh()) return false;
      const redirect = response.headers.get("HX-Redirect");
      if (redirect) {
        location.assign(redirect);
        return false;
      }
      if (!response.ok) {
        showAuthoritativeRecovery("The server returned " + response.status + " while refreshing this monitor view.");
        return false;
      }
      const body = await response.text();
      if (!ownsRefresh()) return false;
      main.innerHTML = body;
      window.htmx?.process(main);
      focusMain();
      return true;
    } catch (error) {
      if (!ownsRefresh() || error?.name === "AbortError") return false;
      showAuthoritativeRecovery("The monitor view could not reach the server. Check the connection and retry.");
      return false;
    } finally {
      if (refreshGeneration === generation && refreshController === controller) refreshController = null;
    }
  }

  function showAuthoritativeRecovery(detail) {
    const main = document.getElementById("main-content");
    if (!main) return;
    const section = document.createElement("section");
    section.id = "history-recovery";
    section.className = "xis-content xis-stack";
    section.setAttribute("role", "alert");
    const heading = document.createElement("h1");
    heading.id = "history-recovery-heading";
    heading.tabIndex = -1;
    heading.textContent = "Monitor state could not be refreshed";
    const description = document.createElement("p");
    description.textContent = detail;
    const retry = document.createElement("button");
    retry.type = "button";
    retry.className = "xis-primary-action xis-recovery-action";
    retry.textContent = "Retry authoritative refresh";
    retry.addEventListener("click", refreshAuthoritative);
    section.append(heading, description, retry);
    main.replaceChildren(section);
    main.scrollTop = 0;
    heading.focus({preventScroll: true});
  }

  function configureMobileNavigation() {
    const trigger = document.querySelector("#mobile-monitoring-panel")?.parentElement?.querySelector("button[aria-controls='mobile-monitoring-panel']");
    if (!trigger || trigger.dataset.xisConfigured) return;
    trigger.dataset.xisConfigured = "true";
    const reflect = () => trigger.setAttribute("aria-label", trigger.getAttribute("aria-expanded") === "true" ? "Close monitoring navigation" : "Open monitoring navigation");
    const header = trigger.closest("header");
    const updateOffset = () => {
      if (!header) return;
      document.documentElement.style.setProperty("--xis-shell-header-bottom", Math.ceil(header.getBoundingClientRect().bottom) + "px");
    };
    new MutationObserver(reflect).observe(trigger, {attributes: true, attributeFilter: ["aria-expanded"]});
    if (header && window.ResizeObserver) new ResizeObserver(updateOffset).observe(header);
    window.addEventListener("resize", updateOffset);
    reflect();
    updateOffset();
    window.addEventListener("keydown", event => {
      if (event.key !== "Escape" || trigger.getAttribute("aria-expanded") !== "true") return;
      trigger.focus({preventScroll: true});
      setTimeout(() => { reflect(); trigger.focus({preventScroll: true}); }, 0);
    }, true);
  }

  document.addEventListener("htmx:beforeRequest", event => {
    invalidateAuthoritativeRefresh();
    rememberFocus(event);
  });
  document.addEventListener("htmx:afterSettle", settle);
  document.addEventListener("htmx:historyRestore", () => setTimeout(refreshAuthoritative, 0));
  window.addEventListener("popstate", invalidateAuthoritativeRefresh);
  window.addEventListener("pageshow", event => { if (event.persisted) refreshAuthoritative(); });
  window.goshtosoDependencies?.ready.then(() => {
    configureMobileNavigation();
    if (document.querySelector("#main-content [data-autofocus]")) focusMain();
  }).catch(() => {});
  document.addEventListener("htmx:afterSettle", configureMobileNavigation);
})();
`

func serveApplicationJS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = io.WriteString(w, applicationJS)
}

func (s *server) route(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.URL.Path {
	case "/":
		if r.Method != http.MethodGet {
			s.methodNotAllowed(w, r, http.MethodGet)
			return
		}
		http.Redirect(w, r, "/monitors", http.StatusSeeOther)
	case "/monitors":
		if r.Method != http.MethodGet {
			s.methodNotAllowed(w, r, http.MethodGet)
			return
		}
		s.monitors(w, r)
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
		r.PostForm.Get("email"),
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
	http.Redirect(w, r, "/monitors", http.StatusSeeOther)
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

func (s *server) monitors(w http.ResponseWriter, r *http.Request) {
	credential, ok := s.cookies.Session(r)
	if !ok {
		s.redirectLogin(w, r)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	currentCursor := r.URL.Query().Get("cursor")
	page, searchedPages, err := s.listMonitorMatches(r.Context(), credential, currentCursor, query)
	if err != nil {
		s.monitorFailure(w, r, credential, err)
		return
	}
	health, failures, unauthorized := s.enrichMonitorHealth(r.Context(), credential, page.Items)
	if unauthorized {
		s.cookies.ClearSession(w)
		s.redirectLogin(w, r)
		return
	}
	csrfToken := s.cookies.SessionCSRF(credential)
	data := view.MonitorList{Monitors: page.Items, Health: health, Cursor: currentCursor, NextCursor: page.NextCursor, Query: query, Selected: r.URL.Query().Get("selected"), HealthFailures: failures, SearchPages: searchedPages}
	s.renderAdaptive(w, r, http.StatusOK, view.MonitorPage(csrfToken, data), view.MonitorContent(data))
}

// listMonitorMatches walks a bounded number of control-plane pages without
// inspecting or manufacturing opaque cursors. A remaining cursor is returned
// so the user can continue the search from exactly where this window stopped.
func (s *server) listMonitorMatches(ctx context.Context, credential, cursor, query string) (sdk.Page[sdk.Monitor], int, error) {
	if query == "" {
		page, err := s.controlPlane.ListMonitors(ctx, credential, cursor, monitorPageSize)
		return page, 1, err
	}

	result := sdk.Page[sdk.Monitor]{}
	next := cursor
	seen := map[string]struct{}{}
	for pages := 1; pages <= maxSearchPageWalk; pages++ {
		if _, duplicate := seen[next]; duplicate {
			return result, pages - 1, fmt.Errorf("monitor pagination returned a repeated cursor")
		}
		seen[next] = struct{}{}
		page, err := s.controlPlane.ListMonitors(ctx, credential, next, monitorPageSize)
		if err != nil {
			return sdk.Page[sdk.Monitor]{}, pages - 1, err
		}
		result.Items = append(result.Items, filterMonitors(page.Items, query)...)
		result.NextCursor = page.NextCursor
		if page.NextCursor == "" || pages == maxSearchPageWalk {
			return result, pages, nil
		}
		next = page.NextCursor
	}
	return result, maxSearchPageWalk, nil
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
	status, err := s.controlPlane.GetPublicStatusPage(r.Context())
	if err != nil {
		problem := view.Problem{Title: "Public status unavailable", Detail: "The latest public status could not be loaded. Existing navigation remains available; retry shortly.", Code: "public_status_unavailable", CorrelationID: correlationID(r.Context()), RetryURL: "/status"}
		code := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) {
			code = http.StatusGatewayTimeout
			problem.Title = "Public status timed out"
		}
		s.renderStateFailure(w, r, code, view.StatusErrorPage(problem), view.PublicStatusError(problem))
		return
	}
	s.renderAdaptive(w, r, http.StatusOK, view.StatusPage(status), view.StatusContent(status))
}

func (s *server) monitorFailure(w http.ResponseWriter, r *http.Request, credential string, err error) {
	if errors.Is(err, controlplane.ErrUnauthorized) || isAPIStatus(err, http.StatusUnauthorized) {
		s.cookies.ClearSession(w)
		s.redirectLogin(w, r)
		return
	}
	problem := view.Problem{Title: "Monitors unavailable", Detail: "The monitor list could not be loaded. Your search is preserved in the URL; retry shortly.", Code: "monitors_unavailable", CorrelationID: correlationID(r.Context()), RetryURL: r.URL.RequestURI()}
	code := http.StatusBadGateway
	if errors.Is(err, context.DeadlineExceeded) {
		code = http.StatusGatewayTimeout
		problem.Title = "Monitor request timed out"
	}
	csrfToken := s.cookies.SessionCSRF(credential)
	s.renderStateFailure(w, r, code, view.MonitorErrorPage(csrfToken, problem), view.MonitorErrorContent(problem))
}

func (s *server) renderStateFailure(w http.ResponseWriter, r *http.Request, status int, full, fragment templ.Component) {
	if isHTMX(r) {
		w.Header().Set("X-Xisnove-Response-Status", fmt.Sprint(status))
		s.renderAdaptive(w, r, http.StatusOK, full, fragment)
		return
	}
	s.renderAdaptive(w, r, status, full, fragment)
}

func (s *server) redirectLogin(w http.ResponseWriter, r *http.Request) {
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", "/login")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func filterMonitors(monitors []sdk.Monitor, query string) []sdk.Monitor {
	needle := strings.ToLower(query)
	filtered := make([]sdk.Monitor, 0, len(monitors))
	for _, monitor := range monitors {
		if strings.Contains(strings.ToLower(monitor.Name), needle) || strings.Contains(strings.ToLower(monitor.Description), needle) || strings.Contains(strings.ToLower(string(monitor.Kind)), needle) {
			filtered = append(filtered, monitor)
		}
	}
	return filtered
}

func (s *server) enrichMonitorHealth(ctx context.Context, credential string, monitors []sdk.Monitor) (map[string]sdk.MonitorHealth, int, bool) {
	const parallelism = 4
	health := make(map[string]sdk.MonitorHealth, len(monitors))
	sem := make(chan struct{}, parallelism)
	var mu sync.Mutex
	var wg sync.WaitGroup
	failures := 0
	unauthorized := false
	for _, monitor := range monitors {
		monitor := monitor
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				failures++
				mu.Unlock()
				return
			}
			defer func() { <-sem }()
			value, err := s.controlPlane.GetMonitorHealth(ctx, credential, monitor.Id)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				health[monitor.Id.String()] = sdk.MonitorHealth{MonitorId: monitor.Id, State: sdk.Unknown}
				failures++
				if errors.Is(err, controlplane.ErrUnauthorized) || isAPIStatus(err, http.StatusUnauthorized) {
					unauthorized = true
				}
				return
			}
			health[monitor.Id.String()] = value
		}()
	}
	wg.Wait()
	return health, failures, unauthorized
}

func isAPIStatus(err error, status int) bool {
	var apiError *sdk.APIError
	return errors.As(err, &apiError) && apiError.StatusCode == status
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
	renderCtx := r.Context()
	if renderCtx.Err() != nil {
		// Preserve request values such as correlation ID while allowing the small
		// terminal recovery surface to render after the upstream deadline fired.
		renderCtx = context.WithoutCancel(renderCtx)
	}
	if err := renderComponent(renderCtx, w, status, component); err != nil {
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
	if credential, ok := s.cookies.Session(r); ok && r.URL.Path != "/login" && r.URL.Path != "/status" {
		csrfToken := s.cookies.SessionCSRF(credential)
		s.renderAdaptive(w, r, p.Status, view.ShellProblemPage(csrfToken, v), view.ShellProblemContent(v))
		return
	}
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
		nonce := s.newCSPNonce()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", fmt.Sprintf("default-src 'self'; script-src 'nonce-%s' 'strict-dynamic' 'unsafe-eval' https://unpkg.com 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'", nonce))
		next.ServeHTTP(w, r.WithContext(templ.WithNonce(r.Context(), nonce)))
	})
}

func (s *server) newCSPNonce() string {
	value := make([]byte, 18)
	if _, err := io.ReadFull(s.random, value); err == nil {
		return base64.RawURLEncoding.EncodeToString(value)
	}
	return fmt.Sprintf("xisnove-%d", s.fallbackID.Add(1))
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
