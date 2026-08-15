package web

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	chartassets "github.com/araihu/goshtoso-charts/assets"
	"github.com/araihu/goshtoso/assets"
	"github.com/araihu/xisnove/sdk"
	"github.com/araihu/xisnove/ui/internal/controlplane"
	"github.com/araihu/xisnove/ui/internal/seasonalassets"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const (
	testUsername   = "local-admin"
	testPassword   = "correct horse battery staple"
	testCredential = "opaque.control-plane/credential"
)

func TestNoneAuthServesProtectedRoutesWithoutSession(t *testing.T) {
	client := controlplane.NewFake("unused", "unused", DevelopmentNoneCredential)
	client.Monitors = []sdk.Monitor{{
		Id: uuid.New(), Name: "Home DNS", Description: "Resolver", Kind: sdk.MonitorKindDns, Enabled: true,
	}}
	handler, _ := newTestHandlerWithModes(t, client, time.Second, []AuthMode{AuthModeNone})

	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/monitors", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Home DNS") {
		t.Fatalf("none-mode monitors = %d %s", recorder.Code, recorder.Body.String())
	}
	if cookies := recorder.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("none-mode monitors issued cookies: %#v", cookies)
	}
	if strings.Contains(recorder.Body.String(), DevelopmentNoneCredential) {
		t.Fatal("none-mode control-plane credential reached browser response")
	}

	loginRequest := httptest.NewRequest(http.MethodGet, "https://ui.example.test/login", nil)
	loginRecorder := httptest.NewRecorder()
	handler.ServeHTTP(loginRecorder, loginRequest)
	if loginRecorder.Code != http.StatusSeeOther || loginRecorder.Header().Get("Location") != "/monitors" {
		t.Fatalf("none-mode login = %d Location %q", loginRecorder.Code, loginRecorder.Header().Get("Location"))
	}
}

func TestMonitorAvailabilityEventsStreamsCompleteChartSnapshot(t *testing.T) {
	monitorID := uuid.MustParse("10000000-0000-4000-8000-000000000099")
	client := controlplane.NewFake("unused", "unused", DevelopmentNoneCredential)
	client.Health[monitorID] = sdk.MonitorHealth{MonitorId: monitorID, State: sdk.Down}
	client.History[monitorID] = sdk.MonitorAvailabilityHistory{MonitorId: monitorID, Samples: []sdk.MonitorAvailabilitySample{
		{Id: uuid.New(), LocationId: uuid.New(), ObservedAt: time.Date(2026, time.August, 15, 11, 0, 0, 0, time.UTC), Outcome: sdk.MonitorAvailabilitySampleOutcomePassed},
		{Id: uuid.New(), LocationId: uuid.New(), ObservedAt: time.Date(2026, time.August, 15, 11, 1, 0, 0, time.UTC), Outcome: sdk.MonitorAvailabilitySampleOutcomeFailed},
	}}
	handler, _ := newTestHandlerWithModes(t, client, 10*time.Millisecond, []AuthMode{AuthModeNone})
	testServer := httptest.NewServer(handler)
	defer testServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, testServer.URL+"/monitors/"+monitorID.String()+"/availability/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := testServer.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("SSE status = %d: %s", response.StatusCode, body)
	}
	if got := response.Header.Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
		t.Fatalf("SSE Content-Type = %q", got)
	}
	reader := bufio.NewReader(response.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != "event: chart\n" {
		t.Fatalf("SSE event line = %q, err=%v", line, err)
	}
	dataLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("SSE data line: %v", err)
	}
	var snapshot struct {
		Categories []string `json:"categories"`
		Series     []struct {
			Name   string    `json:"name"`
			Values []float64 `json:"values"`
		} `json:"series"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(dataLine, "data: "))), &snapshot); err != nil {
		t.Fatalf("decode SSE snapshot: %v; line=%q", err, dataLine)
	}
	if len(snapshot.Categories) != 2 || len(snapshot.Series) != 4 {
		t.Fatalf("SSE snapshot shape = %#v", snapshot)
	}
	if snapshot.Series[0].Name != "Healthy" || len(snapshot.Series[0].Values) != 2 || snapshot.Series[0].Values[0] != 1 || snapshot.Series[0].Values[1] != 0 {
		t.Fatalf("SSE healthy series = %#v", snapshot.Series[0])
	}
	if snapshot.Series[2].Name != "Down" || snapshot.Series[2].Values[0] != 0 || snapshot.Series[2].Values[1] != 1 {
		t.Fatalf("SSE down series = %#v", snapshot.Series[2])
	}
	cancel()
}

func TestMonitorAvailabilityEventsStreamsStateTickProvenanceSnapshot(t *testing.T) {
	monitorID := uuid.MustParse("10000000-0000-4000-8000-000000000098")
	client := controlplane.NewFake("unused", "unused", DevelopmentNoneCredential)
	client.StateHistory[monitorID] = sdk.MonitorStateHistory{
		MonitorId: monitorID,
		Ticks: []sdk.MonitorStateTick{{
			Id:         uuid.MustParse("20000000-0000-0000-0000-000000000098"),
			MonitorId:  monitorID,
			Lifecycle:  sdk.Active,
			Health:     sdk.Degraded,
			ReasonCode: sdk.StateTickReasonCodeDependencyUnknown,
			Actor:      sdk.StateTickActor{Kind: sdk.StateTickActorKindSystem},
			OccurredAt: time.Now().UTC().Add(-time.Minute),
		}},
	}
	handler, _ := newTestHandlerWithModes(t, client, time.Second, []AuthMode{AuthModeNone})
	testServer := httptest.NewServer(handler)
	defer testServer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, testServer.URL+"/monitors/"+monitorID.String()+"/availability/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := testServer.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	if event, err := readSSEEvent(reader); err != nil || event.Name != "chart" {
		t.Fatalf("first SSE event = %#v, err=%v", event, err)
	}
	event, err := readSSEEvent(reader)
	if err != nil {
		t.Fatalf("state SSE event: %v", err)
	}
	if event.Name != "state-ticks" {
		t.Fatalf("second SSE event = %#v, want state-ticks", event)
	}
	var history sdk.MonitorStateHistory
	if err := json.Unmarshal([]byte(event.Data), &history); err != nil {
		t.Fatalf("decode state history event: %v", err)
	}
	if len(history.Ticks) != 1 || history.Ticks[0].ReasonCode != sdk.StateTickReasonCodeDependencyUnknown {
		t.Fatalf("state history event = %#v", history)
	}
}

func TestMonitorAvailabilityEventsSignalsStateHistoryAuthFailureWithoutCredential(t *testing.T) {
	monitorID := uuid.MustParse("10000000-0000-4000-8000-000000000097")
	client := controlplane.NewFake("unused", "unused", DevelopmentNoneCredential)
	client.StateHistoryErrors[monitorID] = controlplane.ErrUnauthorized
	handler, _ := newTestHandlerWithModes(t, client, time.Second, []AuthMode{AuthModeNone})
	testServer := httptest.NewServer(handler)
	defer testServer.Close()

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, testServer.URL+"/monitors/"+monitorID.String()+"/availability/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := testServer.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	if event, err := readSSEEvent(reader); err != nil || event.Name != "chart" {
		t.Fatalf("first SSE event = %#v, err=%v", event, err)
	}
	event, err := readSSEEvent(reader)
	if err != nil {
		t.Fatalf("auth SSE event: %v", err)
	}
	if event.Name != "auth-error" || event.Data != `{"status":401,"code":"unauthorized"}` {
		t.Fatalf("auth SSE event = %#v", event)
	}
	if strings.Contains(event.Data, DevelopmentNoneCredential) {
		t.Fatal("auth SSE event exposed server-side credential")
	}
}

func TestSelectedMonitorStateHistoryPreservesSafeErrorByMonitor(t *testing.T) {
	monitorID := uuid.MustParse("10000000-0000-0000-0000-000000000098")
	monitor := sdk.Monitor{Id: monitorID, Name: "Home DNS"}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "forbidden", err: &sdk.APIError{StatusCode: http.StatusForbidden}, want: "State history unavailable (HTTP 403)."},
		{name: "not found", err: &sdk.APIError{StatusCode: http.StatusNotFound}, want: "State history unavailable (HTTP 404)."},
		{name: "upstream", err: &sdk.APIError{StatusCode: http.StatusBadGateway}, want: "State history unavailable (HTTP 502)."},
		{name: "timeout", err: context.DeadlineExceeded, want: "State history request timed out."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := controlplane.NewFake("unused", "unused", DevelopmentNoneCredential)
			client.StateHistoryErrors[monitorID] = tc.err
			s := &server{controlPlane: client, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

			history, historyErrors, unauthorized := s.selectedMonitorStateHistory(
				context.Background(), DevelopmentNoneCredential, []sdk.Monitor{monitor}, monitorID.String(),
			)
			if unauthorized {
				t.Fatal("state history error marked unauthorized")
			}
			if len(history) != 0 {
				t.Fatalf("history = %#v, want no history on error", history)
			}
			if got := historyErrors[monitorID.String()]; got != tc.want {
				t.Fatalf("history error = %q, want %q", got, tc.want)
			}
		})
	}
}

type sseEvent struct {
	Name string
	Data string
}

func readSSEEvent(reader *bufio.Reader) (sseEvent, error) {
	var event sseEvent
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return event, err
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			return event, nil
		}
		switch {
		case strings.HasPrefix(line, "event: "):
			event.Name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			event.Data = strings.TrimPrefix(line, "data: ")
		}
	}
}

var csrfValuePattern = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

func TestHandlerMountsEveryGoshtosoAndSeasonalRuntimeAssetDirectly(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)
	paths := []string{"/assets/styles.css", "/assets/js/dependency-loader.js", "/assets/js/combobox.js", "/consoleshell/assets/shell.css", "/consoleshell/assets/shell.js", chartassets.RuntimeURL, assets.AlpineCollapseURL, assets.AlpineFocusURL, assets.AlpineMaskURL, assets.AlpineJSURL, assets.HTMXURL}
	for _, descriptor := range seasonalassets.Descriptors() {
		paths = append(paths, descriptor.Path)
	}
	for _, path := range paths {
		request := httptest.NewRequest(http.MethodGet, "https://ui.example.test"+path, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || recorder.Body.Len() == 0 {
			t.Errorf("GET %s = %d, %d bytes", path, recorder.Code, recorder.Body.Len())
		}
	}
}

func TestHandlerServesPinnedSeasonalX9Assets(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)
	for _, descriptor := range seasonalassets.Descriptors() {
		request := httptest.NewRequest(http.MethodGet, "https://ui.example.test"+descriptor.Path, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != descriptor.ContentType {
			t.Errorf("GET %s = %d content-type=%q", descriptor.Path, recorder.Code, recorder.Header().Get("Content-Type"))
		}
		if got := recorder.Header().Get("Cache-Control"); got != descriptor.CacheControl {
			t.Errorf("GET %s cache=%q, want %q", descriptor.Path, got, descriptor.CacheControl)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(recorder.Body.Bytes())); got != descriptor.SHA256 {
			t.Errorf("GET %s SHA-256 = %s, want %s", descriptor.Path, got, descriptor.SHA256)
		}
	}
}

func TestHandlerServesLatestAraiHuThemeAsImmutableCSS(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)
	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/ui/araihu-v0.2.1.css", nil)
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
	for _, want := range []string{`[data-theme="araihu"]`, `--color-primary: #173b72`, `--color-primary-dark: #c7ff4a`} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Errorf("theme body missing %q", want)
		}
	}
	canonicalStart := strings.Index(recorder.Body.String(), "@layer")
	if canonicalStart < 0 {
		t.Fatal("theme body omits canonical @layer block")
	}
	canonicalBody := recorder.Body.String()[canonicalStart:]
	if got, want := fmt.Sprintf("%x", sha256.Sum256([]byte(canonicalBody))), "bda734da2ce1a65badb12221cb74f5cf478359e743a683ab5e17e4330b532f8d"; got != want {
		t.Fatalf("canonical Arai Hû body SHA-256 = %s, want %s", got, want)
	}
	if got, want := fmt.Sprintf("%x", sha256.Sum256(recorder.Body.Bytes())), "9ec3f3187b736252b18f3aefef4737ba2025ef1c637611c3d0ecf58748043f1b"; got != want {
		t.Fatalf("Arai Hû v0.2.1 CSS SHA-256 = %s, want %s", got, want)
	}
}

func TestHandlerKeepsPreviousAraiHuThemeRouteForRollingCompatibility(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)
	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/ui/araihu-f841fe90.css", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("legacy theme status = %d", recorder.Code)
	}
	if got, want := fmt.Sprintf("%x", sha256.Sum256(recorder.Body.Bytes())), "eb47ecd7d3360a3b48f858b60d4c14322011f0aed6a520eb1ce2ecbc56ffb498"; got != want {
		t.Fatalf("legacy Arai Hû CSS SHA-256 = %s, want %s", got, want)
	}
}

func TestHandlerServesCanonicalVersionedXisnoveFavicon(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)
	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/ui/xisnove-ab01f1a.svg", nil)
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
	if got, want := fmt.Sprintf("%x", sha256.Sum256(recorder.Body.Bytes())), "243b21be750d9187318675dc1088d6cd45415758efc2d4bc958b024a6f18c1b8"; got != want {
		t.Fatalf("canonical Xisnove favicon SHA-256 = %s, want %s", got, want)
	}
}

func TestHandlerServesCanonicalV10XisnoveIdentityAssets(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)
	for assetPath, wantHash := range map[string]string{
		"/ui/xisnove-logo-ab01f1a.svg":         "c1d9947e502f4018992f77c9466f7f85d95e8fef4d482b676de9ad0f9470ca3c",
		"/ui/xisnove-mark-ab01f1a.svg":         "0c55914d3d07d8cdab04a6d05da6bc0c7a977ea2f5bf56ff2a3f93ee4e3290bb",
		"/ui/xisnove-mark-reverse-ab01f1a.svg": "57c90a71fadace5136df01230c1aa8cd210071263ac04249cd3aaa694a8ff952",
	} {
		request := httptest.NewRequest(http.MethodGet, "https://ui.example.test"+assetPath, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Errorf("GET %s = %d cache=%q", assetPath, recorder.Code, recorder.Header().Get("Cache-Control"))
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(recorder.Body.Bytes())); got != wantHash {
			t.Errorf("GET %s SHA-256 = %s, want %s", assetPath, got, wantHash)
		}
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
	for _, want := range []string{"<!doctype html>", `/assets/styles.css`, `/ui/araihu-v0.2.1.css`, `/ui/xisnove-ab01f1a.svg`, `data-theme="araihu"`, `type="password"`, `name="_csrf"`} {
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
	for _, want := range []string{"<nav", `class="console-shell-root"`, `id="main-content"`, `action="/logout"`, `name="_csrf"`, `href="/status"`, `/consoleshell/assets/shell.css`, seasonalassets.LogoPath, `data-asset-brand="logo"`, seasonalassets.FaviconPath, `id="account-menu"`, "Sign out", "No monitors yet"} {
		if !strings.Contains(dashboard, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	appearanceNonce := regexp.MustCompile(`<script nonce="([^"]+)">\(function\(o\)`).FindStringSubmatch(dashboard)
	if len(appearanceNonce) != 2 {
		t.Fatalf("console shell appearance bootstrap is missing a request nonce: %s", dashboard)
	}
	if shellNonce := regexp.MustCompile(`<script defer nonce="([^"]+)" src="/consoleshell/assets/shell\.js`).FindStringSubmatch(dashboard); len(shellNonce) != 2 || shellNonce[1] != appearanceNonce[1] {
		t.Fatalf("console shell runtime does not share the request nonce: %s", dashboard)
	}
	if csp := dashboardRecorder.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "'nonce-"+appearanceNonce[1]+"'") {
		t.Fatalf("console shell appearance nonce is not authorized by CSP: %q", csp)
	}
	if strings.Contains(dashboard, testCredential) || strings.Contains(dashboard, sessionCookie.Value) {
		t.Fatal("dashboard exposed the control-plane session credential")
	}
	for _, absent := range []string{`id="theme-choice"`, `id="mode-choice"`, `Monitor tools`, `id="monitor-search"`, "bounded control-plane requests"} {
		if strings.Contains(dashboard, absent) {
			t.Errorf("dashboard retained removed control %q", absent)
		}
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

func TestAuthenticatedShellRendersOneGlobalSearchOwner(t *testing.T) {
	handler, _ := newTestHandler(t, controlplane.NewFake(testUsername, testPassword, testCredential), time.Second)
	session := loginSession(t, handler)
	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/monitors", nil)
	request.AddCookie(session)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	body := recorder.Body.String()
	for _, want := range []string{`id="global-search"`, `aria-controls="global-search-dialog"`, `id="global-search-dialog"`, `id="global-search-input"`, `aria-autocomplete="list"`} {
		if strings.Count(body, want) != 1 {
			t.Errorf("shell count(%q) = %d", want, strings.Count(body, want))
		}
	}
	if strings.Contains(body, `x-on:keydown.window`) {
		t.Fatal("Goshtoso and the BFF must not both own the global search shortcut")
	}
	if strings.Contains(body, `goshtosoSearchField`) {
		t.Fatal("global search trigger must not depend on CSP-blocked inline component runtime")
	}
}

func TestGlobalSearchBFFReturnsBoundedCanonicalMonitorResultsAndRecovery(t *testing.T) {
	monitorID := uuid.MustParse("00000000-0000-4000-8000-000000000091")
	client := controlplane.NewFake(testUsername, testPassword, testCredential)
	client.Monitors = []sdk.Monitor{{Id: monitorID, Name: "Kubernetes API LAN", Description: "Homelab control plane", Kind: sdk.MonitorKindTcp}}
	handler, _ := newTestHandler(t, client, time.Second)
	session := loginSession(t, handler)

	request := httptest.NewRequest(http.MethodGet, "https://ui.example.test/search?q=kubernetes", nil)
	request.AddCookie(session)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("search status = %d: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, want := range []string{`role="option"`, `aria-selected="false"`, "Kubernetes API LAN", `/monitors?selected=00000000-0000-4000-8000-000000000091`, `hx-target="#main-content"`} {
		if !strings.Contains(body, want) {
			t.Errorf("search body missing %q: %s", want, body)
		}
	}

	client.SearchError = errors.New("upstream unavailable")
	errorRequest := httptest.NewRequest(http.MethodGet, "https://ui.example.test/search?q=kubernetes", nil)
	errorRequest.AddCookie(session)
	errorRecorder := httptest.NewRecorder()
	handler.ServeHTTP(errorRecorder, errorRequest)
	if errorRecorder.Code != http.StatusBadGateway || !strings.Contains(errorRecorder.Body.String(), `data-search-retry`) {
		t.Fatalf("search recovery = %d %s", errorRecorder.Code, errorRecorder.Body.String())
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
	for _, want := range []string{`event.metaKey || event.ctrlKey`, `event.key.toLowerCase() !== "k"`, `goshtoso-search-open`, `row?.querySelector("a[href]") || row`} {
		if strings.Count(recorder.Body.String(), want) == 0 {
			t.Errorf("application search shortcut missing %q", want)
		}
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
	wantNames := []string{"alpine-collapse", "alpine-focus", "alpine-mask", "first-party", "alpine", "htmx"}
	if len(config.Dependencies) != len(wantNames) {
		t.Fatalf("dependency count = %d, want %d", len(config.Dependencies), len(wantNames))
	}
	for index, dependency := range config.Dependencies {
		if dependency.Name != wantNames[index] {
			t.Errorf("dependency[%d] = %q, want %q", index, dependency.Name, wantNames[index])
		}
		switch dependency.Name {
		case "first-party":
			if dependency.PrimaryURL != "/assets/js/goshtoso.min.js" || dependency.FallbackURL != "" || dependency.Integrity != "" {
				t.Errorf("first-party source = %#v", dependency)
			}
		default:
			if !strings.HasPrefix(dependency.PrimaryURL, "https://unpkg.com/") || !strings.HasPrefix(dependency.FallbackURL, "/assets/") || !strings.HasPrefix(dependency.Integrity, "sha384-") {
				t.Errorf("dependency[%d] is not pinned CDN-first with SRI and fallback: %#v", index, dependency)
			}
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
	for _, want := range []string{`id="monitor-content"`, `aria-selected="true"`, `data-monitor-id="` + monitorID.String() + `"`, `hx-get="/monitors?q=dns&amp;selected=` + monitorID.String() + `"`, `hx-target="#main-content"`, `hx-swap="outerHTML"`, `hx-push-url="true"`, `role="button"`, `tabindex="0"`, `id="monitor-detail"`, `data-autofocus`, "UNKNOWN", "Some health is unavailable"} {
		if !strings.Contains(body, want) {
			t.Errorf("monitor fragment missing %q", want)
		}
	}
	for _, absent := range []string{`aria-label="Select monitor `, `>Select</a>`} {
		if strings.Contains(body, absent) {
			t.Errorf("monitor fragment retained removed row action %q", absent)
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
	for _, want := range []string{"Remote DNS", "q=dns", "selected=" + matchID.String()} {
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
		`href="/monitors?cursor=offset%3A125&amp;q=dns&amp;selected=kept"`,
		`hx-get="/monitors?cursor=offset%3A25&amp;q=dns&amp;selected=` + matchID.String() + `"`,
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
	return newTestHandlerWithModes(t, client, timeout, nil)
}

func newTestHandlerWithModes(t *testing.T, client controlplane.Client, timeout time.Duration, authModes []AuthMode) (http.Handler, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	handler, err := New(Config{
		ControlPlane:   client,
		CookieSecret:   []byte("0123456789abcdef0123456789abcdef"),
		CookieSecure:   true,
		RequestTimeout: timeout,
		AuthModes:      authModes,
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
func (blockingClient) SearchResources(ctx context.Context, _, _ string, _ int32) ([]sdk.SearchResult, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (blockingClient) ListMonitors(ctx context.Context, _, _ string, _ int32) (sdk.Page[sdk.Monitor], error) {
	<-ctx.Done()
	return sdk.Page[sdk.Monitor]{}, ctx.Err()
}
func (blockingClient) GetMonitorHealth(ctx context.Context, _ string, _ openapi_types.UUID) (sdk.MonitorHealth, error) {
	<-ctx.Done()
	return sdk.MonitorHealth{}, ctx.Err()
}
func (blockingClient) GetMonitorAvailabilityHistory(ctx context.Context, _ string, _ openapi_types.UUID, _ time.Time, _ time.Time, _ int32) (sdk.MonitorAvailabilityHistory, error) {
	<-ctx.Done()
	return sdk.MonitorAvailabilityHistory{}, ctx.Err()
}
func (blockingClient) GetMonitorStateHistory(ctx context.Context, _ string, _ openapi_types.UUID, _ time.Time, _ time.Time, _ int32) (sdk.MonitorStateHistory, error) {
	<-ctx.Done()
	return sdk.MonitorStateHistory{}, ctx.Err()
}
