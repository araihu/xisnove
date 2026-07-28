package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/araihu/goshtoso/assets"
	"github.com/araihu/xisnove/sdk"
	"github.com/araihu/xisnove/ui/internal/controlplane"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const (
	testUsername   = "local-admin"
	testPassword   = "correct horse battery staple"
	testCredential = "opaque.control-plane/credential"
)

var csrfValuePattern = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

func TestHandlerMountsEveryGoshtosoRuntimeAssetDirectly(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)
	for _, path := range []string{"/assets/styles.css", "/assets/js/dependency-loader.js", "/assets/js/combobox.js", assets.AlpineCollapseURL, assets.AlpineFocusURL, assets.AlpineMaskURL, assets.AlpineJSURL, assets.HTMXURL} {
		request := httptest.NewRequest(http.MethodGet, "https://ui.example.test"+path, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || recorder.Body.Len() == 0 {
			t.Errorf("GET %s = %d, %d bytes", path, recorder.Code, recorder.Body.Len())
		}
	}
}

func TestHandlerServesPinnedAraiHuThemeAsImmutableCSS(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)
	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/ui/araihu-a8a9647.css", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("theme status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Fatalf("theme Content-Type = %q", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("theme Cache-Control = %q", got)
	}
	for _, want := range []string{`[data-theme="araihu"]`, `--color-primary: #173b72`, `--color-primary-dark: #c7ff4a`, `--araihu-logo-surface: var(--color-surface)`, `.dark [data-theme="araihu"]`} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Errorf("theme body missing %q", want)
		}
	}
	canonicalStart := strings.Index(recorder.Body.String(), "@layer")
	if canonicalStart < 0 {
		t.Fatal("theme body omits canonical @layer block")
	}
	canonicalBody := recorder.Body.String()[canonicalStart:]
	if got, want := fmt.Sprintf("%x", sha256.Sum256([]byte(canonicalBody))), "3b96e49ffac44a4df90c64286dfb7722a37f576d10097090f8dd4f0a85b54d38"; got != want {
		t.Fatalf("canonical Arai Hû body SHA-256 = %s, want %s", got, want)
	}
}

func TestHandlerServesApprovedV11X9Favicon(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)
	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/ui/x9-v11-icon-9aef3646.svg", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("favicon status = %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "image/svg+xml" {
		t.Fatalf("favicon Content-Type = %q", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("favicon Cache-Control = %q", got)
	}
	if got, want := fmt.Sprintf("%x", sha256.Sum256(recorder.Body.Bytes())), "9aef364637db7ad0bcbee9b86f215bab783f081590f8eab4937c0a6315ef1aea"; got != want {
		t.Fatalf("approved v11 X-9 favicon SHA-256 = %s, want %s", got, want)
	}
}

func TestHandlerRetainsPreviousV3XisnoveFaviconDuringRollout(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)
	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/ui/xisnove-bffc2ac.svg", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("previous v3 favicon status = %d", recorder.Code)
	}
	if got, want := fmt.Sprintf("%x", sha256.Sum256(recorder.Body.Bytes())), "4df17d9b60b9999bed10e1e937ac5fdce433245ff5c4bdf43bd81605a4372d61"; got != want {
		t.Fatalf("previous v3 Xisnove favicon SHA-256 = %s, want %s", got, want)
	}
}

func TestHandlerRetainsPreviousImmutableXisnoveFaviconDuringRollout(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)
	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/ui/xisnove-81300f5.svg", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("previous favicon status = %d", recorder.Code)
	}
	if got, want := fmt.Sprintf("%x", sha256.Sum256(recorder.Body.Bytes())), "4edb4342c4ccffc7f3b8daa79ac883c40ff7524e540f4e5560f5b457edcb8fdd"; got != want {
		t.Fatalf("previous Xisnove favicon SHA-256 = %s, want %s", got, want)
	}
}

func TestLoginPageUsesBundledHeadAndDoesNotRenderCredentials(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)
	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/login", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	for _, want := range []string{"<!doctype html>", `/assets/styles.css`, `/ui/araihu-a8a9647.css`, `/ui/x9-v11-icon-9aef3646.svg`, `data-theme="araihu"`, `type="password"`, `name="_csrf"`} {
		if !strings.Contains(body, want) {
			t.Errorf("login body missing %q", want)
		}
	}
	for _, secret := range []string{testUsername, testPassword, testCredential} {
		if strings.Contains(body, secret) {
			t.Errorf("login body exposed %q", secret)
		}
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Host-xisnove-login-csrf" || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatalf("login CSRF cookie = %#v", cookies)
	}
}

func TestLoginShellAndLogoutJourneyKeepsOpaqueCredentialOutOfMarkup(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)
	loginCSRF, loginCSRFCookie := getLoginCSRF(t, handler)

	loginForm := url.Values{
		"email":    {testUsername},
		"password": {testPassword},
		"_csrf":    {loginCSRF},
	}
	loginRequest := httptest.NewRequest(http.MethodPost, "https://ui.example.test/login", strings.NewReader(loginForm.Encode()))
	loginRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRequest.AddCookie(loginCSRFCookie)
	loginRecorder := httptest.NewRecorder()
	handler.ServeHTTP(loginRecorder, loginRequest)

	if loginRecorder.Code != http.StatusSeeOther || loginRecorder.Header().Get("Location") != "/monitors" {
		t.Fatalf("login response = %d Location %q", loginRecorder.Code, loginRecorder.Header().Get("Location"))
	}
	sessionCookie := cookieNamed(t, loginRecorder.Result().Cookies(), "__Host-xisnove-session")
	if !sessionCookie.Secure || !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie lost security flags: %#v", sessionCookie)
	}

	dashboardRequest := httptest.NewRequest(http.MethodGet, "https://ui.example.test/monitors", nil)
	dashboardRequest.AddCookie(sessionCookie)
	dashboardRecorder := httptest.NewRecorder()
	handler.ServeHTTP(dashboardRecorder, dashboardRequest)
	if dashboardRecorder.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d", dashboardRecorder.Code)
	}
	dashboard := dashboardRecorder.Body.String()
	for _, want := range []string{"<nav", `id="main-content"`, `action="/logout"`, `name="_csrf"`, "No monitors yet"} {
		if !strings.Contains(dashboard, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	if strings.Contains(dashboard, testCredential) || strings.Contains(dashboard, sessionCookie.Value) {
		t.Fatal("dashboard exposed the control-plane session credential")
	}

	logoutCSRF := csrfFromBody(t, dashboard)
	logoutForm := url.Values{"_csrf": {logoutCSRF}}
	logoutRequest := httptest.NewRequest(http.MethodPost, "https://ui.example.test/logout", strings.NewReader(logoutForm.Encode()))
	logoutRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	logoutRequest.AddCookie(sessionCookie)
	logoutRecorder := httptest.NewRecorder()
	handler.ServeHTTP(logoutRecorder, logoutRequest)

	if logoutRecorder.Code != http.StatusSeeOther || logoutRecorder.Header().Get("Location") != "/login" {
		t.Fatalf("logout response = %d Location %q", logoutRecorder.Code, logoutRecorder.Header().Get("Location"))
	}
	cleared := cookieNamed(t, logoutRecorder.Result().Cookies(), "__Host-xisnove-session")
	if cleared.MaxAge >= 0 || cleared.Expires.IsZero() {
		t.Fatalf("logout did not expire session cookie: %#v", cleared)
	}
}

func TestPublicStatusRendersFullPageOrHTMXFragment(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)

	fullRequest := httptest.NewRequest(http.MethodGet, "https://ui.example.test/status", nil)
	fullRecorder := httptest.NewRecorder()
	handler.ServeHTTP(fullRecorder, fullRequest)
	if fullRecorder.Code != http.StatusOK || !strings.Contains(fullRecorder.Body.String(), "<!doctype html>") {
		t.Fatalf("full status response = %d %q", fullRecorder.Code, fullRecorder.Body.String())
	}
	if !strings.Contains(fullRecorder.Body.String(), `id="status-content"`) {
		t.Fatal("full status page omitted status content")
	}

	fragmentRequest := httptest.NewRequest(http.MethodGet, "https://ui.example.test/status", nil)
	fragmentRequest.Header.Set("HX-Request", "true")
	fragmentRecorder := httptest.NewRecorder()
	handler.ServeHTTP(fragmentRecorder, fragmentRequest)
	if fragmentRecorder.Code != http.StatusOK {
		t.Fatalf("fragment status = %d", fragmentRecorder.Code)
	}
	fragment := fragmentRecorder.Body.String()
	if strings.Contains(fragment, "<html") || !strings.Contains(fragment, `id="status-content"`) {
		t.Fatalf("unexpected status fragment %q", fragment)
	}
	if !strings.Contains(fragmentRecorder.Header().Get("Vary"), "HX-Request") {
		t.Fatalf("Vary = %q", fragmentRecorder.Header().Get("Vary"))
	}
}

func TestAuthenticatedUnknownRouteKeepsShellAndNoStoreRecovery(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)
	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/unknown-workspace", nil)
	request.AddCookie(loginSession(t, handler))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unknown route = %d, Cache-Control %q", recorder.Code, recorder.Header().Get("Cache-Control"))
	}
	for _, want := range []string{`id="main-content"`, `id="problem-content"`, `href="/monitors"`, "Return to monitors"} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Errorf("in-shell recovery missing %q", want)
		}
	}
}

func TestApplicationScriptIsSameOriginAndCSPCompatible(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)
	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/ui/app.js", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/javascript") || !strings.Contains(recorder.Body.String(), "htmx:afterSettle") {
		t.Fatalf("application script = %d %q %q", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String())
	}
	for _, want := range []string{"script-src 'nonce-", "'strict-dynamic'", "'unsafe-eval'", "https://unpkg.com", "'self'", "connect-src 'self'"} {
		if got := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(got, want) {
			t.Errorf("Content-Security-Policy = %q, missing %q", got, want)
		}
	}
}

func TestLoginPageUsesOrderedCDNFirstDependenciesWithNonceAndFallback(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)
	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/login", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	body := recorder.Body.String()

	match := regexp.MustCompile(`data-goshtoso-dependencies="([^"]+)"`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("dependency loader config missing: %s", body)
	}
	var config struct {
		Dependencies []struct {
			Name        string `json:"name"`
			PrimaryURL  string `json:"primary_url"`
			FallbackURL string `json:"fallback_url"`
			Integrity   string `json:"integrity"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(html.UnescapeString(match[1])), &config); err != nil {
		t.Fatalf("decode dependency config: %v", err)
	}
	wantNames := []string{"alpine-collapse", "alpine-focus", "alpine-mask", "alpine", "htmx", "combobox"}
	if len(config.Dependencies) != len(wantNames) {
		t.Fatalf("dependency count = %d, want %d", len(config.Dependencies), len(wantNames))
	}
	for index, dependency := range config.Dependencies {
		if dependency.Name != wantNames[index] {
			t.Errorf("dependency[%d] = %q, want %q", index, dependency.Name, wantNames[index])
		}
		if index < 5 {
			if !strings.HasPrefix(dependency.PrimaryURL, "https://unpkg.com/") || !strings.HasPrefix(dependency.FallbackURL, "/assets/") || !strings.HasPrefix(dependency.Integrity, "sha384-") {
				t.Errorf("dependency[%d] is not pinned CDN-first with SRI and fallback: %#v", index, dependency)
			}
		} else if dependency.PrimaryURL != "/assets/js/combobox.js" || dependency.FallbackURL != "" {
			t.Errorf("combobox source = %#v", dependency)
		}
	}
	nonceMatch := regexp.MustCompile(`src="/assets/js/dependency-loader.js"[^>]* nonce="([^"]+)"`).FindStringSubmatch(body)
	if len(nonceMatch) != 2 || !strings.Contains(body, `src="/ui/app.js" defer nonce="`+nonceMatch[1]+`"`) {
		t.Fatalf("loader and application script do not share request nonce: %s", body)
	}
	if csp := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "'nonce-"+nonceMatch[1]+"'") {
		t.Fatalf("CSP nonce does not match rendered scripts: %q", csp)
	}
}

func TestMonitorOperationsListPreservesFiltersAndPartialHealth(t *testing.T) {
	client := controlplane.NewFake(testUsername, testPassword, testCredential)
	monitorID := uuid.New()
	client.Monitors = []sdk.Monitor{{Id: monitorID, Name: "Home DNS", Description: "Resolver", Kind: sdk.MonitorKindDns, Enabled: true}}
	client.HealthErrors[monitorID] = errors.New("health backend unavailable")
	handler, _ := newTestHandler(t, client, time.Second)
	sessionCookie := loginSession(t, handler)

	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/monitors?q=dns&selected="+monitorID.String(), nil)
	request.Header.Set("HX-Request", "true")
	request.AddCookie(sessionCookie)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{`id="monitor-content"`, `value="dns"`, `aria-selected="true"`, `data-monitor-id="` + monitorID.String() + `"`, `id="monitor-detail"`, `data-autofocus`, "UNKNOWN", "Some health is unavailable", `hx-target="#main-content"`} {
		if !strings.Contains(body, want) {
			t.Errorf("monitor fragment missing %q", want)
		}
	}
	if strings.Contains(body, "<html") || strings.Contains(body, testCredential) {
		t.Fatal("fragment included shell or bearer")
	}
}

func TestMonitorSearchWalksPastEmptyPageAndPreservesOpaqueState(t *testing.T) {
	client := controlplane.NewFake(testUsername, testPassword, testCredential)
	for index := 0; index < 25; index++ {
		client.Monitors = append(client.Monitors, sdk.Monitor{Id: uuid.New(), Name: fmt.Sprintf("HTTP monitor %02d", index), Kind: sdk.MonitorKindHttp, Enabled: true})
	}
	matchID := uuid.New()
	client.Monitors = append(client.Monitors, sdk.Monitor{Id: matchID, Name: "Remote DNS", Kind: sdk.MonitorKindDns, Enabled: true})
	handler, _ := newTestHandler(t, client, time.Second)
	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/monitors?q=dns&selected=kept", nil)
	request.Header.Set("HX-Request", "true")
	request.AddCookie(loginSession(t, handler))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{"Remote DNS", "across 2 API page(s)", "bounded to 4 pages", "q=dns", "selected=" + matchID.String()} {
		if !strings.Contains(body, want) {
			t.Errorf("search response missing %q", want)
		}
	}
}

func TestMonitorSearchEmptyWindowStillOffersOpaqueContinuation(t *testing.T) {
	client := controlplane.NewFake(testUsername, testPassword, testCredential)
	for index := 0; index < 151; index++ {
		client.Monitors = append(client.Monitors, sdk.Monitor{Id: uuid.New(), Name: fmt.Sprintf("HTTP monitor %03d", index), Kind: sdk.MonitorKindHttp, Enabled: true})
	}
	matchID := client.Monitors[30].Id
	client.Monitors[30].Name = "Remote DNS"
	client.Monitors[30].Kind = sdk.MonitorKindDns
	handler, _ := newTestHandler(t, client, time.Second)
	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/monitors?cursor=offset%3A25&q=dns&selected=kept", nil)
	request.Header.Set("HX-Request", "true")
	request.AddCookie(loginSession(t, handler))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	body := recorder.Body.String()
	for _, want := range []string{
		"Remote DNS",
		"Next page",
		`href="/monitors?cursor=offset%3A25&amp;q=dns&amp;selected=kept"`,
		`href="/monitors?cursor=offset%3A125&amp;q=dns&amp;selected=kept"`,
		`href="/monitors?cursor=offset%3A25&amp;q=dns&amp;selected=` + matchID.String() + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("stateful continuation response missing %q", want)
		}
	}
}

type repeatedCursorClient struct {
	*controlplane.Fake
	calls int
}

func (c *repeatedCursorClient) ListMonitors(ctx context.Context, credential, cursor string, limit int32) (sdk.Page[sdk.Monitor], error) {
	c.calls++
	return sdk.Page[sdk.Monitor]{NextCursor: cursor}, nil
}

func TestMonitorSearchRejectsRepeatedOpaqueCursor(t *testing.T) {
	client := &repeatedCursorClient{Fake: controlplane.NewFake(testUsername, testPassword, testCredential)}
	handler, _ := newTestHandler(t, client, time.Second)
	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/monitors?cursor=opaque-cycle&q=dns&selected=kept", nil)
	request.Header.Set("HX-Request", "true")
	request.AddCookie(loginSession(t, handler))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || recorder.Header().Get("X-Xisnove-Response-Status") != "502" {
		t.Fatalf("response = %d headers %#v", recorder.Code, recorder.Header())
	}
	if client.calls != 1 {
		t.Fatalf("ListMonitors calls = %d, want 1 before repeated cursor rejection", client.calls)
	}
	body := recorder.Body.String()
	for _, want := range []string{"Monitors unavailable", "cursor=opaque-cycle", "q=dns", "selected=kept"} {
		if !strings.Contains(body, want) {
			t.Errorf("repeated-cursor response missing %q", want)
		}
	}
}

func TestMonitorHTMXFailureSwapsScopedRetryWithOriginalQuery(t *testing.T) {
	client := controlplane.NewFake(testUsername, testPassword, testCredential)
	client.MonitorError = errors.New("database offline")
	handler, _ := newTestHandler(t, client, time.Second)
	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/monitors?q=router", nil)
	request.Header.Set("HX-Request", "true")
	request.AddCookie(loginSession(t, handler))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("X-Xisnove-Response-Status") != "502" {
		t.Fatalf("response = %d headers %#v", recorder.Code, recorder.Header())
	}
	for _, want := range []string{"Monitors unavailable", `/monitors?q=router`, "Retry"} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestPublicStatusHTMXFailureIsExplicitlySwappable(t *testing.T) {
	client := controlplane.NewFake(testUsername, testPassword, testCredential)
	client.PublicError = context.DeadlineExceeded
	handler, _ := newTestHandler(t, client, time.Second)
	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/status", nil)
	request.Header.Set("HX-Request", "true")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("X-Xisnove-Response-Status") != "504" || !strings.Contains(recorder.Body.String(), "Public status timed out") {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestMethodProblemUsesRFC9457ShapeAndCorrelation(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)
	request := httptest.NewRequest(http.MethodPost, "https://ui.example.test/status", nil)
	request.Header.Set("Accept", "application/problem+json")
	request.Header.Set("X-Request-ID", "request-123")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", recorder.Code)
	}
	if recorder.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("Content-Type = %q", recorder.Header().Get("Content-Type"))
	}
	if recorder.Header().Get("X-Request-ID") != "request-123" {
		t.Fatalf("X-Request-ID = %q", recorder.Header().Get("X-Request-ID"))
	}
	var problem map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	for key, want := range map[string]any{
		"type":           "urn:xisnove:ui:problem:method-not-allowed",
		"title":          "Method not allowed",
		"status":         float64(http.StatusMethodNotAllowed),
		"code":           "method_not_allowed",
		"correlation_id": "request-123",
		"instance":       "/status",
	} {
		if problem[key] != want {
			t.Errorf("problem[%q] = %#v, want %#v", key, problem[key], want)
		}
	}
}

func TestLoginTimeoutIsPresentedWithoutLoggingCredentialsOrQuery(t *testing.T) {
	client := blockingClient{}
	handler, logs := newTestHandler(t, client, 5*time.Millisecond)
	loginCSRF, loginCSRFCookie := getLoginCSRF(t, handler)
	form := url.Values{
		"email":    {testUsername},
		"password": {testPassword},
		"_csrf":    {loginCSRF},
	}
	request := httptest.NewRequest(http.MethodPost, "https://ui.example.test/login?token=query-secret", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/problem+json")
	request.AddCookie(loginCSRFCookie)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504: %s", recorder.Code, recorder.Body.String())
	}
	for _, secret := range []string{testUsername, testPassword, testCredential, "query-secret"} {
		if strings.Contains(recorder.Body.String(), secret) || strings.Contains(logs.String(), secret) {
			t.Errorf("response or log exposed %q", secret)
		}
	}
	for _, field := range []string{`"method":"POST"`, `"path":"/login"`, `"correlation_id":`} {
		if !strings.Contains(logs.String(), field) {
			t.Errorf("access log missing %s: %s", field, logs.String())
		}
	}
}

func TestLoginTimeoutRendersHTMLAfterRequestContextDeadline(t *testing.T) {
	handler, _ := newTestHandler(t, blockingClient{}, 5*time.Millisecond)
	loginCSRF, loginCSRFCookie := getLoginCSRF(t, handler)
	form := url.Values{"email": {testUsername}, "password": {testPassword}, "_csrf": {loginCSRF}}
	request := httptest.NewRequest(http.MethodPost, "https://ui.example.test/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(loginCSRFCookie)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusGatewayTimeout || !strings.Contains(recorder.Body.String(), `id="problem-content"`) || !strings.Contains(recorder.Body.String(), "Control plane timed out") {
		t.Fatalf("timeout HTML = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestLogoutRejectsMissingCSRFWithProblemPresentation(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)
	sessionCookie := &http.Cookie{
		Name:  "__Host-xisnove-session",
		Value: "b3BhcXVlLmNvbnRyb2wtcGxhbmUvY3JlZGVudGlhbA",
	}
	request := httptest.NewRequest(http.MethodPost, "https://ui.example.test/logout", strings.NewReader(""))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/problem+json")
	request.AddCookie(sessionCookie)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), `"code":"csrf_failed"`) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func newTestHandler(t *testing.T, client controlplane.Client, timeout time.Duration) (http.Handler, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	handler, err := New(Config{
		ControlPlane:   client,
		CookieSecret:   []byte("0123456789abcdef0123456789abcdef"),
		CookieSecure:   true,
		RequestTimeout: timeout,
		Random:         bytes.NewReader(bytes.Repeat([]byte{0x2a}, 4096)),
		Logger:         slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	return handler, &logs
}

func getLoginCSRF(t *testing.T, handler http.Handler) (string, *http.Cookie) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/login", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get login status = %d: %s", recorder.Code, recorder.Body.String())
	}
	return csrfFromBody(t, recorder.Body.String()), cookieNamed(t, recorder.Result().Cookies(), "__Host-xisnove-login-csrf")
}

func loginSession(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	csrf, csrfCookie := getLoginCSRF(t, handler)
	form := url.Values{"email": {testUsername}, "password": {testPassword}, "_csrf": {csrf}}
	request := httptest.NewRequest(http.MethodPost, "https://ui.example.test/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(csrfCookie)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("login = %d %s", recorder.Code, recorder.Body.String())
	}
	return cookieNamed(t, recorder.Result().Cookies(), "__Host-xisnove-session")
}

func csrfFromBody(t *testing.T, body string) string {
	t.Helper()
	match := csrfValuePattern.FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("CSRF token not found in body: %s", body)
	}
	return match[1]
}

func cookieNamed(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("cookie %q not found in %#v", name, cookies)
	return nil
}

type blockingClient struct{}

func (blockingClient) ExchangeAdministratorCredentials(ctx context.Context, _, _ string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func (blockingClient) RevokeSession(context.Context, string) error { return nil }
func (blockingClient) GetPublicStatusPage(ctx context.Context) (sdk.PublicStatusPage, error) {
	<-ctx.Done()
	return sdk.PublicStatusPage{}, ctx.Err()
}
func (blockingClient) ListMonitors(ctx context.Context, _, _ string, _ int32) (sdk.Page[sdk.Monitor], error) {
	<-ctx.Done()
	return sdk.Page[sdk.Monitor]{}, ctx.Err()
}
func (blockingClient) GetMonitorHealth(ctx context.Context, _ string, _ openapi_types.UUID) (sdk.MonitorHealth, error) {
	<-ctx.Done()
	return sdk.MonitorHealth{}, ctx.Err()
}
