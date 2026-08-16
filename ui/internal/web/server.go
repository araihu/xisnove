package web

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a-h/templ"
	shellassets "github.com/araihu/goshtoso-app-shells/consoleshell/assets"
	chartassets "github.com/araihu/goshtoso-charts/assets"
	"github.com/araihu/goshtoso/assets"
	"github.com/araihu/xisnove/sdk"
	"github.com/araihu/xisnove/ui/internal/availability"
	"github.com/araihu/xisnove/ui/internal/controlplane"
	"github.com/araihu/xisnove/ui/internal/seasonalassets"
	"github.com/araihu/xisnove/ui/internal/security"
	"github.com/araihu/xisnove/ui/internal/view"
	"github.com/google/uuid"
)

const (
	maxFormBytes             = 64 << 10
	monitorPageSize          = int32(25)
	locationPageSize         = int32(50)
	maxSearchPageWalk        = 4
	availabilityPollInterval = 5 * time.Second
	stateHistoryLimit        = controlplane.StateHistoryMaxRecords
)

type AuthMode string

const (
	AuthModeBasic AuthMode = "basic"
	AuthModeNone  AuthMode = "none"
	AuthModeOIDC  AuthMode = "oidc"

	// DevelopmentNoneCredential stays server-side. It lets the local fake
	// control-plane exercise protected reads without placing a bearer token in
	// the browser.
	DevelopmentNoneCredential = "development-none"
)

type Config struct {
	ControlPlane   controlplane.Client
	CookieSecret   []byte
	CookieSecure   bool
	RequestTimeout time.Duration
	AuthModes      []AuthMode
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
	noneAuth     bool
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
	authModes := cfg.AuthModes
	if len(authModes) == 0 {
		authModes = []AuthMode{AuthModeBasic}
	}
	for _, mode := range authModes {
		if mode == AuthModeNone {
			s.noneAuth = true
			break
		}
	}

	mux := http.NewServeMux()
	mux.Handle("GET /assets/", assets.Handler())
	mux.Handle("GET /assets/campaign/", seasonalassets.Handler())
	mux.Handle("GET "+chartassets.Prefix, chartassets.Handler())
	mux.Handle("GET /consoleshell/assets/", shellassets.Handler())
	mux.Handle("GET /ui/seasonal/", seasonalassets.Handler())
	// Keep the previous immutable theme URL available during a rolling
	// deployment while new pages move to the v0.2.1 asset bytes.
	mux.HandleFunc("GET /ui/araihu-v0.2.1.css", serveAraiHuThemeCSS)
	mux.HandleFunc("GET /ui/araihu-f841fe90.css", serveLegacyAraiHuThemeCSS)
	mux.HandleFunc("GET /ui/xisnove-ab01f1a.svg", serveXisnoveFavicon)
	mux.HandleFunc("GET /ui/xisnove-logo-ab01f1a.svg", serveXisnoveLogo)
	mux.HandleFunc("GET /ui/xisnove-mark-ab01f1a.svg", serveXisnoveMark)
	mux.HandleFunc("GET /ui/xisnove-mark-reverse-ab01f1a.svg", serveXisnoveReverseMark)
	mux.HandleFunc("GET /ui/xisnove-bffc2ac.svg", servePreviousV3XisnoveFavicon)
	mux.HandleFunc("GET /ui/xisnove-81300f5.svg", servePreviousXisnoveFavicon)
	mux.HandleFunc("GET /ui/app.js", serveApplicationJS)
	mux.HandleFunc("GET /monitors/{monitorID}/availability/events", s.monitorAvailabilityEvents)
	mux.HandleFunc("GET /monitors/{monitorID}", s.monitorDetail)
	mux.HandleFunc("/", s.route)

	var handler http.Handler = mux
	handler = s.recoverPanics(handler)
	handler = s.securityHeaders(handler)
	handler = s.accessLog(handler)
	handler = s.requestTimeout(handler)
	handler = s.correlation(handler)
	return handler, nil
}

//go:embed static/araihu-v0.2.1.css
var araiHuThemeCSS string

//go:embed static/araihu-f841fe90.css
var legacyAraiHuThemeCSS string

//go:embed static/xisnove-favicon.svg
var xisnoveFavicon string

//go:embed static/xisnove-logo.svg
var xisnoveLogo string

//go:embed static/xisnove-mark.svg
var xisnoveMark string

//go:embed static/xisnove-mark-reverse.svg
var xisnoveReverseMark string

//go:embed static/xisnove-favicon-bffc2ac.svg
var previousV3XisnoveFavicon string

//go:embed static/xisnove-favicon-81300f5.svg
var previousXisnoveFavicon string

func serveXisnoveFavicon(w http.ResponseWriter, _ *http.Request) {
	serveImmutableSVG(w, xisnoveFavicon)
}

func serveXisnoveLogo(w http.ResponseWriter, _ *http.Request) {
	serveImmutableSVG(w, xisnoveLogo)
}

func serveXisnoveMark(w http.ResponseWriter, _ *http.Request) {
	serveImmutableSVG(w, xisnoveMark)
}

func serveXisnoveReverseMark(w http.ResponseWriter, _ *http.Request) {
	serveImmutableSVG(w, xisnoveReverseMark)
}

func servePreviousV3XisnoveFavicon(w http.ResponseWriter, _ *http.Request) {
	serveImmutableSVG(w, previousV3XisnoveFavicon)
}

func serveImmutableSVG(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = io.WriteString(w, body)
}

func servePreviousXisnoveFavicon(w http.ResponseWriter, _ *http.Request) {
	serveImmutableSVG(w, previousXisnoveFavicon)
}

func serveAraiHuThemeCSS(w http.ResponseWriter, _ *http.Request) {
	serveImmutableCSS(w, araiHuThemeCSS)
}

func serveLegacyAraiHuThemeCSS(w http.ResponseWriter, _ *http.Request) {
	serveImmutableCSS(w, legacyAraiHuThemeCSS)
}

func serveImmutableCSS(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = io.WriteString(w, body)
}

const applicationJS = `(() => {
  if (window.__xisnoveApplicationScriptInstalled) return;
  window.__xisnoveApplicationScriptInstalled = true;
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
    if (heading) document.title = heading.textContent.trim() + " · X-9";
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
		const template = document.createElement("template");
		template.innerHTML = body;
		const replacement = template.content.querySelector("main#main-content");
		if (!replacement) {
			if (template.content.querySelector("section")) {
				main.replaceChildren(...Array.from(template.content.childNodes));
				window.htmx?.process(main);
				focusMain();
				return true;
			}
			showAuthoritativeRecovery("The server returned an incomplete monitor view while refreshing.");
			return false;
		}
		const sidebar = template.content.querySelector('[hx-swap-oob="outerHTML:#consoleshell-sidebar-content"]');
		main.replaceWith(replacement);
		if (sidebar) document.querySelector("#consoleshell-sidebar-content")?.replaceWith(sidebar);
		const nextMain = document.getElementById("main-content");
		window.htmx?.process(nextMain || replacement);
		window.consoleShell?.reconcileNavigation?.(nextMain || replacement);
		window.dispatchEvent(new CustomEvent("consoleshell:navigated"));
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

  function configureGlobalSearch() {
    const dialog = document.getElementById("global-search-dialog");
    const input = document.getElementById("global-search-input");
    const results = document.getElementById("global-search-results");
    const trigger = document.querySelector("#global-search button");
    if (!dialog || !input || !results || dialog.dataset.xisConfigured) return;
    dialog.dataset.xisConfigured = "true";
    let timer = null;
    let controller = null;
    let generation = 0;
    let activeIndex = -1;
    let restoreFocus = true;

    const options = () => Array.from(results.querySelectorAll('[role="option"]'));
    const select = index => {
      const items = options();
      if (!items.length) {
        activeIndex = -1;
        input.removeAttribute("aria-activedescendant");
        return;
      }
      activeIndex = (index + items.length) % items.length;
      items.forEach((item, itemIndex) => item.setAttribute("aria-selected", itemIndex === activeIndex ? "true" : "false"));
      input.setAttribute("aria-activedescendant", items[activeIndex].id);
      items[activeIndex].scrollIntoView({block: "nearest"});
    };
    const state = (kind, message) => {
      results.replaceChildren();
      const region = document.createElement("div");
      region.className = "xis-global-search-state";
      region.dataset.searchState = kind;
      region.textContent = message;
      results.append(region);
      results.setAttribute("aria-busy", kind === "loading" ? "true" : "false");
      activeIndex = -1;
      input.removeAttribute("aria-activedescendant");
    };
    const search = async () => {
      const query = input.value.trim();
      controller?.abort();
      controller = null;
      generation += 1;
      if (Array.from(query).length < 2) {
        state("prompt", "Type at least 2 characters to search the control plane.");
        return;
      }
      const requestGeneration = generation;
      const request = new AbortController();
      controller = request;
      state("loading", "Searching the control plane…");
      try {
        const response = await fetch("/search?q=" + encodeURIComponent(query), {headers: {"Accept": "text/html"}, cache: "no-store", credentials: "same-origin", signal: request.signal});
        const body = await response.text();
        if (request.signal.aborted || requestGeneration !== generation || input.value.trim() !== query || !dialog.open) return;
        results.innerHTML = body;
        results.setAttribute("aria-busy", "false");
        window.htmx?.process(results);
        select(0);
      } catch (error) {
        if (request.signal.aborted || requestGeneration !== generation || error?.name === "AbortError") return;
        state("error", "Search could not reach the server. Retry or edit the query.");
        const retry = document.createElement("button");
        retry.type = "button";
        retry.className = "xis-recovery-action";
        retry.dataset.searchRetry = "";
        retry.textContent = "Retry search";
        results.firstElementChild?.append(retry);
      } finally {
        if (controller === request) controller = null;
      }
    };
    const close = shouldRestore => {
      restoreFocus = shouldRestore;
      controller?.abort();
      generation += 1;
      if (dialog.open) dialog.close();
    };
	const open = () => {
	  restoreFocus = true;
	  if (!dialog.open) dialog.showModal();
	  trigger?.setAttribute("aria-expanded", "true");
	  requestAnimationFrame(() => input.focus({preventScroll: true}));
	};

    window.addEventListener("goshtoso-search-open", event => {
      if (event.detail?.id !== "global-search") return;
	  open();
    });
	trigger?.addEventListener("click", open);
    window.addEventListener("keydown", event => {
      if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== "k") return;
      event.preventDefault();
      window.dispatchEvent(new CustomEvent("goshtoso-search-open", {detail: {id: "global-search"}}));
    });
    dialog.addEventListener("close", () => {
      clearTimeout(timer);
      controller?.abort();
      generation += 1;
      input.value = "";
      state("prompt", "Type at least 2 characters to search the control plane.");
	  trigger?.setAttribute("aria-expanded", "false");
      window.dispatchEvent(new CustomEvent("goshtoso-search-close", {detail: {id: "global-search"}}));
	  if (restoreFocus) requestAnimationFrame(() => trigger?.focus({preventScroll: true}));
      restoreFocus = true;
    });
	dialog.addEventListener("keydown", event => {
		if (event.key === "Escape") event.stopPropagation();
	}, true);
    dialog.addEventListener("click", event => { if (event.target === dialog) close(true); });
    dialog.querySelector("[data-search-close]")?.addEventListener("click", () => close(true));
    input.addEventListener("input", () => {
      clearTimeout(timer);
      timer = setTimeout(search, 220);
    });
    input.addEventListener("keydown", event => {
      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault();
        select(activeIndex + (event.key === "ArrowDown" ? 1 : -1));
      } else if (event.key === "Enter" && activeIndex >= 0) {
        event.preventDefault();
        options()[activeIndex]?.click();
      }
    });
    results.addEventListener("click", event => {
      if (event.target.closest("[data-search-retry]")) {
        search();
        return;
      }
      if (event.target.closest('[role="option"]')) close(false);
    });
  }

  document.addEventListener("htmx:beforeRequest", event => {
    invalidateAuthoritativeRefresh();
    rememberFocus(event);
  });
  document.addEventListener("htmx:afterSettle", settle);
  document.addEventListener("htmx:historyRestore", () => setTimeout(() => {
	refreshAuthoritative();
  }, 0));
  window.addEventListener("popstate", invalidateAuthoritativeRefresh);
  window.addEventListener("pageshow", event => { if (event.persisted) refreshAuthoritative(); });
	configureGlobalSearch();
  window.goshtosoDependencies?.ready.then(() => {
	configureGlobalSearch();
    if (document.querySelector("#main-content [data-autofocus]")) focusMain();
  }).catch(() => {});
  document.addEventListener("htmx:afterSettle", () => {
	configureGlobalSearch();
  });
	document.addEventListener("keydown", event => {
		if (event.key !== "Escape" || document.querySelector("#global-search-dialog")?.open) return;
		const trigger = document.querySelector('button[aria-controls="consoleshell-sidebar"]');
		if (trigger?.getAttribute("aria-expanded") !== "true") return;
		requestAnimationFrame(() => trigger.focus({preventScroll: true}));
	}, true);
	document.addEventListener("keyup", event => {
		if (event.key !== "Escape" || document.querySelector("#global-search-dialog")?.open) return;
		const trigger = document.querySelector('button[aria-controls="consoleshell-sidebar"]');
		if (trigger?.getAttribute("aria-expanded") === "false") trigger.focus({preventScroll: true});
	});
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
	case "/locations":
		s.locations(w, r)
	case "/search":
		if r.Method != http.MethodGet {
			s.methodNotAllowed(w, r, http.MethodGet)
			return
		}
		s.search(w, r)
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
		if strings.HasPrefix(r.URL.Path, "/locations/") {
			s.locationResource(w, r)
			return
		}
		s.writeProblem(w, r, problem{
			Type:   "urn:xisnove:ui:problem:not-found",
			Title:  "Page not found",
			Status: http.StatusNotFound,
			Detail: "The requested UI page does not exist.",
			Code:   "not_found",
		})
	}
}

func (s *server) monitorAvailabilityEvents(w http.ResponseWriter, r *http.Request) {
	credential, ok := s.authCredential(r)
	if !ok {
		w.Header().Set("X-Xisnove-App-Status", "401")
		http.Error(w, "sign-in required", http.StatusUnauthorized)
		return
	}
	monitorID, err := uuid.Parse(r.PathValue("monitorID"))
	if err != nil {
		http.Error(w, "invalid monitor id", http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is unavailable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	send := func() bool {
		windowEnd := time.Now().UTC()
		history := availability.NewHistory(availability.HistoryWindow)
		pollCtx, cancel := context.WithTimeout(r.Context(), s.timeout)
		availabilityHistory, historyErr := s.controlPlane.GetMonitorAvailabilityHistory(
			pollCtx, credential, monitorID, windowEnd.Add(-availability.HistoryLookback), windowEnd, availability.HistoryWindow,
		)
		stateHistory, stateHistoryErr := s.controlPlane.GetMonitorStateHistory(
			pollCtx, credential, monitorID, windowEnd.Add(-controlplane.StateHistoryLookback), windowEnd, stateHistoryLimit,
		)
		if stateHistoryErr == nil {
			stateHistory = controlplane.BoundStateHistory(
				stateHistory,
				monitorID,
				windowEnd.Add(-controlplane.StateHistoryLookback),
				windowEnd,
				stateHistoryLimit,
			)
		}
		if historyErr == nil {
			for _, sample := range availabilityHistory.Samples {
				history.AddOutcome(sample.Outcome, sample.ObservedAt)
			}
		}
		if historyErr != nil || len(availabilityHistory.Samples) == 0 {
			health, healthErr := s.controlPlane.GetMonitorHealth(pollCtx, credential, monitorID)
			state := sdk.Unknown
			if healthErr == nil {
				state = health.State
			} else if historyErr == nil || (!errors.Is(healthErr, context.Canceled) && !errors.Is(healthErr, context.DeadlineExceeded)) {
				s.logger.WarnContext(r.Context(), "availability health poll failed", "monitor_id", monitorID.String(), "error", healthErr)
			}
			history.AddUnknownWindow(windowEnd.Add(-availability.HistoryLookback), windowEnd)
			history.Add(state, windowEnd)
		}
		cancel()
		authFailure := errors.Is(historyErr, controlplane.ErrUnauthorized) || errors.Is(stateHistoryErr, controlplane.ErrUnauthorized)
		if authFailure && !s.noneAuth {
			// Clear the browser session before writing any SSE bytes; headers are
			// committed by the first chart event and cannot expire the cookie later.
			s.cookies.ClearSession(w)
		}
		if historyErr != nil && !errors.Is(historyErr, context.Canceled) && !errors.Is(historyErr, context.DeadlineExceeded) {
			s.logger.WarnContext(r.Context(), "availability history fetch failed", "monitor_id", monitorID.String(), "error", historyErr)
		}
		payload, marshalErr := json.Marshal(history.Snapshot())
		if marshalErr != nil {
			s.logger.ErrorContext(r.Context(), "availability snapshot encode failed", "monitor_id", monitorID.String(), "error", marshalErr)
			return false
		}
		if _, writeErr := fmt.Fprintf(w, "event: chart\ndata: %s\n\n", payload); writeErr != nil {
			return false
		}
		if authFailure {
			if _, writeErr := io.WriteString(w, "event: auth-error\ndata: {\"status\":401,\"code\":\"unauthorized\"}\n\n"); writeErr != nil {
				return false
			}
			flusher.Flush()
			return false
		}
		if stateHistoryErr == nil {
			statePayload, marshalErr := json.Marshal(stateHistory)
			if marshalErr != nil {
				s.logger.ErrorContext(r.Context(), "state history encode failed", "monitor_id", monitorID.String(), "error", marshalErr)
				return false
			}
			if _, writeErr := fmt.Fprintf(w, "event: state-ticks\ndata: %s\n\n", statePayload); writeErr != nil {
				return false
			}
		}
		flusher.Flush()
		if stateHistoryErr != nil && !errors.Is(stateHistoryErr, context.Canceled) && !errors.Is(stateHistoryErr, context.DeadlineExceeded) {
			s.logger.WarnContext(r.Context(), "state history fetch failed", "monitor_id", monitorID.String(), "error", stateHistoryErr)
		}
		return true
	}

	if !send() {
		return
	}
	ticker := time.NewTicker(availabilityPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !send() {
				return
			}
		}
	}
}

func (s *server) search(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	credential, ok := s.authCredential(r)
	if !ok {
		w.Header().Set("X-Xisnove-App-Status", "401")
		w.WriteHeader(http.StatusUnauthorized)
		_ = view.GlobalSearchResults(view.GlobalSearchData{Error: "Your session expired. Sign in again to search."}).Render(r.Context(), w)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	results, err := s.controlPlane.SearchResources(r.Context(), credential, query, 8)
	if err != nil {
		status := http.StatusBadGateway
		message := "Search is temporarily unavailable. Retry or edit the query."
		if errors.Is(err, controlplane.ErrUnauthorized) {
			s.cookies.ClearSession(w)
			status = http.StatusUnauthorized
			message = "Your session expired. Sign in again to search."
		}
		w.Header().Set("X-Xisnove-App-Status", fmt.Sprint(status))
		w.WriteHeader(status)
		_ = view.GlobalSearchResults(view.GlobalSearchData{Query: query, Error: message}).Render(r.Context(), w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = view.GlobalSearchResults(view.GlobalSearchData{Query: query, Items: results}).Render(r.Context(), w)
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	if s.noneAuth {
		http.Redirect(w, r, "/monitors", http.StatusSeeOther)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if _, ok := s.authCredential(r); ok {
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
	if s.noneAuth {
		http.Redirect(w, r, "/monitors", http.StatusSeeOther)
		return
	}
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
	credential, ok := s.authCredential(r)
	if !ok {
		s.redirectLogin(w, r)
		return
	}
	if selected := strings.TrimSpace(r.URL.Query().Get("selected")); selected != "" {
		if monitorID, err := uuid.Parse(selected); err == nil {
			s.renderMonitorDetail(w, r, credential, monitorID)
			return
		}
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
	stateHistory, stateHistoryErrors, stateHistoryUnauthorized := s.selectedMonitorStateHistory(r.Context(), credential, page.Items, r.URL.Query().Get("selected"))
	if stateHistoryUnauthorized {
		s.cookies.ClearSession(w)
		s.redirectLogin(w, r)
		return
	}
	csrfToken := s.cookies.SessionCSRF(credential)
	data := view.MonitorList{Monitors: page.Items, Health: health, StateHistory: stateHistory, StateHistoryErrors: stateHistoryErrors, Cursor: currentCursor, NextCursor: page.NextCursor, Query: query, Selected: r.URL.Query().Get("selected"), HealthFailures: failures, SearchPages: searchedPages}
	s.renderAdaptive(w, r, http.StatusOK, view.MonitorPage(csrfToken, data), view.ConsoleFragment("Monitors", csrfToken, view.MonitorContent(data)))
}

func (s *server) monitorDetail(w http.ResponseWriter, r *http.Request) {
	credential, ok := s.authCredential(r)
	if !ok {
		s.redirectLogin(w, r)
		return
	}
	monitorID, err := uuid.Parse(strings.TrimSpace(r.PathValue("monitorID")))
	if err != nil {
		s.writeProblem(w, r, invalidRequestProblem())
		return
	}
	s.renderMonitorDetail(w, r, credential, monitorID)
}

func (s *server) renderMonitorDetail(w http.ResponseWriter, r *http.Request, credential string, monitorID uuid.UUID) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	cursor := r.URL.Query().Get("cursor")
	page, _, err := s.listMonitorMatches(r.Context(), credential, cursor, monitorID.String())
	if err != nil {
		s.monitorDetailFailure(w, r, credential, query, cursor, err)
		return
	}
	var monitor sdk.Monitor
	found := false
	for _, candidate := range page.Items {
		if candidate.Id == monitorID {
			monitor = candidate
			found = true
			break
		}
	}
	if !found {
		s.monitorDetailFailure(w, r, credential, query, cursor, fmt.Errorf("monitor %s was not found", monitorID))
		return
	}
	health, failures, unauthorized := s.enrichMonitorHealth(r.Context(), credential, []sdk.Monitor{monitor})
	if unauthorized {
		s.cookies.ClearSession(w)
		s.redirectLogin(w, r)
		return
	}
	stateHistory, stateHistoryErrors, stateHistoryUnauthorized := s.selectedMonitorStateHistory(r.Context(), credential, []sdk.Monitor{monitor}, monitorID.String())
	if stateHistoryUnauthorized {
		s.cookies.ClearSession(w)
		s.redirectLogin(w, r)
		return
	}
	value := health[monitorID.String()]
	if value.State == "" {
		value = sdk.MonitorHealth{MonitorId: monitorID, State: sdk.Unknown}
		health[monitorID.String()] = value
	}
	csrfToken := s.cookies.SessionCSRF(credential)
	data := view.MonitorList{
		Monitors: []sdk.Monitor{monitor}, Health: health, StateHistory: stateHistory, StateHistoryErrors: stateHistoryErrors,
		Query: query, Cursor: cursor, Selected: monitorID.String(), HealthFailures: failures,
	}
	s.renderAdaptive(w, r, http.StatusOK, view.MonitorDetailPage(csrfToken, data, monitor, value), view.ConsoleFragment("Monitor detail", csrfToken, view.MonitorDetailContent(data, monitor, value)))
}

func (s *server) monitorDetailFailure(w http.ResponseWriter, r *http.Request, credential, query, cursor string, err error) {
	if errors.Is(err, controlplane.ErrUnauthorized) || isAPIStatus(err, http.StatusUnauthorized) {
		s.cookies.ClearSession(w)
		s.redirectLogin(w, r)
		return
	}
	problem := view.Problem{Title: "Monitor unavailable", Detail: "The selected monitor could not be loaded. Return to the monitor list and retry shortly.", Code: "monitor_unavailable", CorrelationID: correlationID(r.Context()), RetryURL: monitorListURL(query, cursor)}
	status := http.StatusNotFound
	if errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusGatewayTimeout
		problem.Title = "Monitor request timed out"
	}
	csrfToken := s.cookies.SessionCSRF(credential)
	s.renderStateFailure(w, r, status, view.MonitorDetailErrorPage(csrfToken, problem), view.MonitorDetailErrorContent(problem))
}

func monitorListURL(query, cursor string) string {
	values := url.Values{}
	if query != "" {
		values.Set("q", query)
	}
	if cursor != "" {
		values.Set("cursor", cursor)
	}
	if encoded := values.Encode(); encoded != "" {
		return "/monitors?" + encoded
	}
	return "/monitors"
}

func (s *server) locations(w http.ResponseWriter, r *http.Request) {
	credential, ok := s.authCredential(r)
	if !ok {
		s.redirectLogin(w, r)
		return
	}
	client, ok := s.controlPlane.(controlplane.LocationClient)
	if !ok {
		s.locationFailure(w, r, credential, errors.New("location management is unavailable"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.renderLocations(w, r, credential, client)
	case http.MethodPost:
		s.createLocation(w, r, credential, client)
	default:
		s.methodNotAllowed(w, r, http.MethodGet, http.MethodPost)
	}
}

func (s *server) renderLocations(w http.ResponseWriter, r *http.Request, credential string, client controlplane.LocationClient) {
	cursor := r.URL.Query().Get("cursor")
	page, err := client.ListLocations(r.Context(), credential, cursor, locationPageSize)
	if err != nil {
		s.locationFailure(w, r, credential, err)
		return
	}
	csrfToken := s.cookies.SessionCSRF(credential)
	data := view.LocationList{Locations: page.Items, Cursor: cursor, NextCursor: page.NextCursor, Selected: strings.TrimSpace(r.URL.Query().Get("selected"))}
	s.renderAdaptive(w, r, http.StatusOK, view.LocationPage(csrfToken, data), view.ConsoleFragment("Locations", csrfToken, view.LocationContent(csrfToken, data)))
}

func (s *server) locationResource(w http.ResponseWriter, r *http.Request) {
	credential, ok := s.authCredential(r)
	if !ok {
		s.redirectLogin(w, r)
		return
	}
	client, ok := s.controlPlane.(controlplane.LocationClient)
	if !ok {
		s.locationFailure(w, r, credential, errors.New("location management is unavailable"))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/locations/")
	if strings.HasSuffix(path, "/disable") {
		idValue := strings.TrimSuffix(path, "/disable")
		locationID, err := uuid.Parse(strings.TrimSuffix(idValue, "/"))
		if err != nil {
			s.writeProblem(w, r, invalidRequestProblem())
			return
		}
		if r.Method != http.MethodPost {
			s.methodNotAllowed(w, r, http.MethodPost)
			return
		}
		s.disableLocation(w, r, credential, client, locationID)
		return
	}
	locationID, err := uuid.Parse(strings.TrimSuffix(path, "/"))
	if err != nil {
		s.writeProblem(w, r, invalidRequestProblem())
		return
	}
	if r.Method == http.MethodGet {
		http.Redirect(w, r, "/locations?selected="+url.QueryEscape(locationID.String()), http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodPost {
		s.methodNotAllowed(w, r, http.MethodPost)
		return
	}
	s.updateLocation(w, r, credential, client, locationID)
}

func (s *server) createLocation(w http.ResponseWriter, r *http.Request, credential string, client controlplane.LocationClient) {
	if err := parseForm(w, r); err != nil {
		s.writeProblem(w, r, invalidRequestProblem())
		return
	}
	if !s.cookies.ValidateSessionCSRF(credential, r.PostForm.Get("_csrf")) {
		s.writeProblem(w, r, csrfProblem())
		return
	}
	input, err := parseLocationInput(r.PostForm, true)
	if err != nil {
		s.writeProblem(w, r, invalidRequestProblem())
		return
	}
	if _, err := client.CreateLocation(r.Context(), credential, input); err != nil {
		s.locationMutationFailure(w, r, err)
		return
	}
	s.redirectLocations(w, r)
}

func (s *server) updateLocation(w http.ResponseWriter, r *http.Request, credential string, client controlplane.LocationClient, locationID uuid.UUID) {
	if err := parseForm(w, r); err != nil {
		s.writeProblem(w, r, invalidRequestProblem())
		return
	}
	if !s.cookies.ValidateSessionCSRF(credential, r.PostForm.Get("_csrf")) {
		s.writeProblem(w, r, csrfProblem())
		return
	}
	input, err := parseLocationInput(r.PostForm, false)
	if err != nil {
		s.writeProblem(w, r, invalidRequestProblem())
		return
	}
	if _, err := client.UpdateLocation(r.Context(), credential, locationID, input); err != nil {
		s.locationMutationFailure(w, r, err)
		return
	}
	s.redirectLocations(w, r)
}

func parseLocationInput(form url.Values, create bool) (controlplane.LocationInput, error) {
	name := strings.TrimSpace(form.Get("name"))
	address := strings.TrimSpace(form.Get("address"))
	protocol := strings.TrimSpace(form.Get("protocol"))
	if name == "" {
		return controlplane.LocationInput{}, errors.New("location name is required")
	}
	if !create && protocol == "" {
		return controlplane.LocationInput{}, errors.New("location protocol is required")
	}
	policy := &sdk.LocationPolicyInput{}
	hasPolicy := false
	parse := func(key string, target **int32) error {
		value := strings.TrimSpace(form.Get(key))
		if value == "" {
			return nil
		}
		parsed, err := strconv.ParseInt(value, 10, 32)
		if err != nil || parsed <= 0 {
			return errors.New("invalid location policy")
		}
		converted := int32(parsed)
		*target = &converted
		hasPolicy = true
		return nil
	}
	if err := parse("intervalSeconds", &policy.IntervalSeconds); err != nil {
		return controlplane.LocationInput{}, err
	}
	if err := parse("timeoutMillis", &policy.TimeoutMillis); err != nil {
		return controlplane.LocationInput{}, err
	}
	if err := parse("failureThreshold", &policy.FailureThreshold); err != nil {
		return controlplane.LocationInput{}, err
	}
	if err := parse("recoveryThreshold", &policy.RecoveryThreshold); err != nil {
		return controlplane.LocationInput{}, err
	}
	return controlplane.LocationInput{
		Name: name, Address: address, Protocol: protocol,
		Policy: func() *sdk.LocationPolicyInput {
			if hasPolicy {
				return policy
			}
			return nil
		}(),
		Enabled: form.Get("enabled") == "true",
	}, nil
}

func (s *server) disableLocation(w http.ResponseWriter, r *http.Request, credential string, client controlplane.LocationClient, locationID uuid.UUID) {
	if err := parseForm(w, r); err != nil {
		s.writeProblem(w, r, invalidRequestProblem())
		return
	}
	if !s.cookies.ValidateSessionCSRF(credential, r.PostForm.Get("_csrf")) {
		s.writeProblem(w, r, csrfProblem())
		return
	}
	if err := client.DisableLocation(r.Context(), credential, locationID); err != nil {
		s.locationMutationFailure(w, r, err)
		return
	}
	s.redirectLocations(w, r)
}

func (s *server) redirectLocations(w http.ResponseWriter, r *http.Request) {
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", "/locations")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/locations", http.StatusSeeOther)
}

func (s *server) locationFailure(w http.ResponseWriter, r *http.Request, credential string, err error) {
	if errors.Is(err, controlplane.ErrUnauthorized) || isAPIStatus(err, http.StatusUnauthorized) {
		s.cookies.ClearSession(w)
		s.redirectLogin(w, r)
		return
	}
	problem := view.Problem{Title: "Locations unavailable", Detail: "The location list could not be loaded. Retry shortly.", Code: "locations_unavailable", CorrelationID: correlationID(r.Context()), RetryURL: r.URL.RequestURI()}
	code := http.StatusBadGateway
	if errors.Is(err, context.DeadlineExceeded) {
		code = http.StatusGatewayTimeout
		problem.Title = "Location request timed out"
	}
	csrfToken := s.cookies.SessionCSRF(credential)
	s.renderStateFailure(w, r, code, view.LocationErrorPage(csrfToken, problem), view.ConsoleFragment("Locations", csrfToken, view.LocationErrorContent(problem)))
}

func (s *server) locationMutationFailure(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, controlplane.ErrUnauthorized) || isAPIStatus(err, http.StatusUnauthorized) {
		s.cookies.ClearSession(w)
		s.redirectLogin(w, r)
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		s.writeProblem(w, r, gatewayTimeoutProblem())
		return
	}
	if isAPIStatus(err, http.StatusBadRequest) || isAPIStatus(err, http.StatusUnprocessableEntity) {
		s.writeProblem(w, r, invalidRequestProblem())
		return
	}
	s.writeProblem(w, r, upstreamProblem())
}

// selectedMonitorStateHistory fetches history only for the selected monitor;
// the list remains a bounded inventory read and does not fan out one history
// request per row. Non-auth upstream failures leave the monitor list usable
// and are preserved as safe per-monitor messages instead of an empty history.
func (s *server) selectedMonitorStateHistory(ctx context.Context, credential string, monitors []sdk.Monitor, selected string) (map[string]sdk.MonitorStateHistory, map[string]string, bool) {
	history := make(map[string]sdk.MonitorStateHistory)
	historyErrors := make(map[string]string)
	selectedID, err := uuid.Parse(strings.TrimSpace(selected))
	if err != nil {
		return history, historyErrors, false
	}
	var monitor sdk.Monitor
	found := false
	for _, candidate := range monitors {
		if candidate.Id == selectedID {
			monitor = candidate
			found = true
			break
		}
	}
	if !found {
		return history, historyErrors, false
	}
	startsAt, endsAt := controlplane.StateHistoryWindow(time.Now().UTC())
	value, err := s.controlPlane.GetMonitorStateHistory(ctx, credential, monitor.Id, startsAt, endsAt, stateHistoryLimit)
	if err != nil {
		if errors.Is(err, controlplane.ErrUnauthorized) || isAPIStatus(err, http.StatusUnauthorized) {
			return history, historyErrors, true
		}
		historyErrors[monitor.Id.String()] = safeStateHistoryError(err)
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			s.logger.WarnContext(ctx, "state history fetch failed", "monitor_id", monitor.Id.String(), "error", err)
		}
		return history, historyErrors, false
	}
	history[monitor.Id.String()] = controlplane.BoundStateHistory(value, monitor.Id, startsAt, endsAt, stateHistoryLimit)
	return history, historyErrors, false
}

func safeStateHistoryError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "State history request timed out."
	}
	var apiErr *sdk.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= http.StatusBadRequest && apiErr.StatusCode <= 599 {
		return fmt.Sprintf("State history unavailable (HTTP %d).", apiErr.StatusCode)
	}
	return "State history unavailable."
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
	credential, ok := s.authCredential(r)
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
	if !s.noneAuth {
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
	}
	s.cookies.ClearSession(w)
	if s.noneAuth {
		http.Redirect(w, r, "/monitors", http.StatusSeeOther)
		return
	}
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
	s.renderStateFailure(w, r, code, view.MonitorErrorPage(csrfToken, problem), view.ConsoleFragment("Monitors", csrfToken, view.MonitorErrorContent(problem)))
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
	if s.noneAuth {
		s.writeProblem(w, r, unauthorizedProblem())
		return
	}
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
		if strings.Contains(strings.ToLower(monitor.Id.String()), needle) || strings.Contains(strings.ToLower(monitor.Name), needle) || strings.Contains(strings.ToLower(monitor.Description), needle) || strings.Contains(strings.ToLower(string(monitor.Kind)), needle) {
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
	if credential, ok := s.authCredential(r); ok && r.URL.Path != "/login" && r.URL.Path != "/status" {
		csrfToken := s.cookies.SessionCSRF(credential)
		s.renderAdaptive(w, r, p.Status, view.ShellProblemPage(csrfToken, v), view.ConsoleFragment(p.Title, csrfToken, view.ShellProblemContent(v)))
		return
	}
	s.renderAdaptive(w, r, p.Status, view.ProblemPage(v), view.ProblemContent(v))
}

func (s *server) authCredential(r *http.Request) (string, bool) {
	if s.noneAuth {
		return DevelopmentNoneCredential, true
	}
	return s.cookies.Session(r)
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
		if strings.HasSuffix(r.URL.Path, "/availability/events") {
			next.ServeHTTP(w, r)
			return
		}
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

func (w *statusRecorder) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
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
