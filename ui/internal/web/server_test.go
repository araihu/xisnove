package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

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

func TestHandlerMountsGoshtosoAssetsDirectly(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)
	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/assets/styles.css", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if !strings.Contains(recorder.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("Content-Type = %q", recorder.Header().Get("Content-Type"))
	}
	if recorder.Body.Len() < 10_000 {
		t.Fatalf("asset body is unexpectedly small: %d bytes", recorder.Body.Len())
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
	for _, want := range []string{"<!doctype html>", `/assets/styles.css`, `type="password"`, `name="_csrf"`} {
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
	for _, want := range []string{`id="monitor-content"`, `value="dns"`, "Home DNS (selected)", "UNKNOWN", "Some health is unavailable", `hx-target="#main-content"`} {
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
	for index := 0; index < 126; index++ {
		client.Monitors = append(client.Monitors, sdk.Monitor{Id: uuid.New(), Name: fmt.Sprintf("HTTP monitor %03d", index), Kind: sdk.MonitorKindHttp, Enabled: true})
	}
	handler, _ := newTestHandler(t, client, time.Second)
	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/monitors?q=dns&selected=kept", nil)
	request.Header.Set("HX-Request", "true")
	request.AddCookie(loginSession(t, handler))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	body := recorder.Body.String()
	for _, want := range []string{"No matching monitors", "Next page", "cursor=offset%3A100", "q=dns", "selected=kept"} {
		if !strings.Contains(body, want) {
			t.Errorf("continuation response missing %q", want)
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
