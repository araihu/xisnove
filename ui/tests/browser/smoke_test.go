//go:build browser

package browser_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/xisnove/sdk"
	"github.com/araihu/xisnove/ui/internal/controlplane"
	"github.com/araihu/xisnove/ui/internal/web"
	cdinput "github.com/chromedp/cdproto/input"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func TestIntegratedBrowserSmoke(t *testing.T) {
	monitorID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	unknownID := uuid.MustParse("10000000-0000-4000-8000-000000000002")
	locationID := uuid.MustParse("20000000-0000-4000-8000-000000000001")
	observedAt := time.Date(2026, time.July, 26, 12, 30, 0, 0, time.UTC)
	homeDNS := sdk.Monitor{Id: monitorID, Name: "Home DNS", Description: "Resolver reachability", Kind: sdk.MonitorKindDns, Enabled: true, Public: true, LocationId: locationID, RequiredLocation: true, IntervalSeconds: 60, TimeoutMillis: 2500, FailureThreshold: 3, RecoveryThreshold: 2, UpdatedAt: observedAt}
	vpsEdge := sdk.Monitor{Id: unknownID, Name: "VPS edge", Description: "External ingress", Kind: sdk.MonitorKindHttp, Enabled: true, LocationId: locationID, IntervalSeconds: 30, TimeoutMillis: 1500, FailureThreshold: 2, RecoveryThreshold: 2, UpdatedAt: observedAt}
	var scenario atomic.Value
	scenario.Store("success")
	var monitorRequests atomic.Int32
	var homeRevision atomic.Int64
	var homeHealth atomic.Value
	homeHealth.Store(string(sdk.Up))
	apiCalls := make([]string, 0, 8)
	var apiMu sync.Mutex
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiMu.Lock()
		apiCalls = append(apiCalls, r.Method+" "+r.URL.RequestURI())
		apiMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sessions":
			var request sdk.CreateSessionRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request.Password != nil && *request.Password == "invalid" {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(sdk.Problem{Type: "urn:test:invalid", Title: "Invalid credentials", Status: http.StatusUnauthorized, Code: "invalid_credentials", CorrelationId: "browser-test"})
				return
			}
			if request.Password != nil && *request.Password == "timeout" {
				time.Sleep(time.Second)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(sdk.Session{Token: "browser-bearer", ExpiresAt: time.Now().Add(time.Hour)})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/sessions/current":
			requireBearer(t, r)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/status-page":
			switch scenario.Load().(string) {
			case "public-empty":
				_ = json.NewEncoder(w).Encode(sdk.PublicStatusPage{GeneratedAt: time.Now(), State: sdk.Unknown})
			case "public-unknown":
				_ = json.NewEncoder(w).Encode(sdk.PublicStatusPage{GeneratedAt: time.Now(), State: sdk.Unknown, Monitors: []sdk.PublicStatusMonitor{{Id: unknownID, Name: "VPS edge", State: sdk.Unknown}}})
			case "public-up":
				_ = json.NewEncoder(w).Encode(sdk.PublicStatusPage{GeneratedAt: time.Now(), State: sdk.Up, Monitors: []sdk.PublicStatusMonitor{{Id: monitorID, Name: "Home DNS", State: sdk.Up}}})
			case "public-error":
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`{"title":"upstream offline"}`))
			case "public-timeout":
				time.Sleep(time.Second)
			default:
				_ = json.NewEncoder(w).Encode(sdk.PublicStatusPage{GeneratedAt: time.Now(), State: sdk.Degraded, Monitors: []sdk.PublicStatusMonitor{{Id: monitorID, Name: "Home DNS", Description: "Resolver reachability", State: sdk.Up}}, ActiveIncidents: []sdk.PublicIncidentSummary{{Id: uuid.New(), MonitorId: monitorID, MonitorName: "Home DNS", OpenedAt: time.Now(), LastTransitionAt: time.Now(), Severity: sdk.Critical, State: sdk.PublicIncidentSummaryStateOpen}}})
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v1/search":
			requireBearer(t, r)
			query := r.URL.Query().Get("q")
			if query == "slow" {
				time.Sleep(500 * time.Millisecond)
			}
			if query == "error" {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`{"title":"search offline"}`))
				return
			}
			items := []sdk.SearchResult{}
			if strings.Contains(strings.ToLower(homeDNS.Name+" "+homeDNS.Description), strings.ToLower(query)) {
				items = append(items, sdk.SearchResult{ResourceType: sdk.SearchResourceTypeMonitor, ResourceId: monitorID, Title: homeDNS.Name, Description: homeDNS.Description, Context: "DNS monitor"})
			}
			if query == "slow" {
				items = append(items, sdk.SearchResult{ResourceType: sdk.SearchResourceTypeMonitor, ResourceId: unknownID, Title: vpsEdge.Name, Description: vpsEdge.Description, Context: "HTTP monitor"})
			}
			_ = json.NewEncoder(w).Encode(sdk.SearchResultPage{Items: items})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/monitors":
			monitorRequests.Add(1)
			requireBearer(t, r)
			currentHome := homeDNS
			revision := homeRevision.Load()
			currentHome.UpdatedAt = observedAt.Add(time.Duration(revision) * time.Minute)
			currentHome.Description = fmt.Sprintf("Resolver reachability revision %d", revision)
			switch scenario.Load().(string) {
			case "monitors-loading":
				time.Sleep(600 * time.Millisecond)
				_ = json.NewEncoder(w).Encode(sdk.MonitorPage{Items: []sdk.Monitor{currentHome}, Page: sdk.PageMetadata{}})
			case "monitors-empty":
				_ = json.NewEncoder(w).Encode(sdk.MonitorPage{Page: sdk.PageMetadata{}})
			case "monitors-filtered":
				_ = json.NewEncoder(w).Encode(sdk.MonitorPage{Items: []sdk.Monitor{{Id: monitorID, Name: "HTTP edge", Kind: sdk.MonitorKindHttp, Enabled: true}}, Page: sdk.PageMetadata{}})
			case "monitors-error":
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`{"title":"upstream offline"}`))
			default:
				_ = json.NewEncoder(w).Encode(sdk.MonitorPage{Items: []sdk.Monitor{currentHome, vpsEdge}, Page: sdk.PageMetadata{}})
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v1/monitors/"+monitorID.String()+"/health":
			requireBearer(t, r)
			if scenario.Load().(string) == "monitors-partial" {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_ = json.NewEncoder(w).Encode(sdk.MonitorHealth{MonitorId: monitorID, State: sdk.HealthState(homeHealth.Load().(string)), LastTransitionAt: observedAt.Add(time.Duration(homeRevision.Load()) * time.Minute)})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/monitors/"+unknownID.String()+"/health":
			requireBearer(t, r)
			_ = json.NewEncoder(w).Encode(sdk.MonitorHealth{MonitorId: unknownID, State: sdk.Unknown, LastTransitionAt: time.Now()})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	adapter, err := controlplane.NewSDKClient(api.URL, api.Client())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := web.New(web.Config{ControlPlane: adapter, CookieSecret: []byte("0123456789abcdef0123456789abcdef"), CookieSecure: true, RequestTimeout: 900 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ui := httptest.NewTLSServer(handler)
	defer ui.Close()

	browser := browserBinary(t)
	allocator, cancelAllocator := chromedp.NewExecAllocator(t.Context(), append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browser), chromedp.Flag("headless", true), chromedp.Flag("ignore-certificate-errors", true), chromedp.Flag("disable-background-networking", true), chromedp.NoSandbox, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck)...)
	defer cancelAllocator()
	ctx, cancel := chromedp.NewContext(allocator)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, 10*time.Minute)
	defer cancelTimeout()

	var consoleMu sync.Mutex
	var consoleProblems []string
	chromedp.ListenTarget(ctx, func(event any) {
		switch value := event.(type) {
		case *cdpruntime.EventExceptionThrown:
			detail := value.ExceptionDetails.Text
			if value.ExceptionDetails.Exception != nil && value.ExceptionDetails.Exception.Description != "" {
				detail = value.ExceptionDetails.Exception.Description
			}
			consoleMu.Lock()
			consoleProblems = append(consoleProblems, detail)
			consoleMu.Unlock()
		case *cdpruntime.EventConsoleAPICalled:
			if value.Type == cdpruntime.APITypeError || value.Type == cdpruntime.APITypeWarning {
				consoleMu.Lock()
				consoleProblems = append(consoleProblems, fmt.Sprintf("%s: %v", value.Type, value.Args))
				consoleMu.Unlock()
			}
		}
	})

	screenshotDir := os.Getenv("XISNOVE_UI_BROWSER_SCREENSHOT_DIR")
	if screenshotDir == "" {
		screenshotDir = t.TempDir()
	}
	if err := os.MkdirAll(screenshotDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := chromedp.Run(ctx, chromedp.Navigate(ui.URL+"/login"), chromedp.WaitVisible("#email")); err != nil {
		t.Fatal(err)
	}
	var defaultTheme bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.documentElement.dataset.theme === 'araihu' && localStorage.getItem('xisnove-theme') === 'araihu'`, &defaultTheme)); err != nil || !defaultTheme {
		t.Fatalf("Arai Hû did not initialize as the default theme: applied=%t err=%v", defaultTheme, err)
	}
	assertSequentialKeyboardTraversal(t, ctx, "login")
	assertKeyboardActivation(t, ctx, `a[href="/status"]`, "#status-content")
	if err := chromedp.Run(ctx, chromedp.Navigate(ui.URL+"/login"), chromedp.WaitVisible("#email")); err != nil {
		t.Fatal(err)
	}
	captureMatrix(t, ctx, screenshotDir, "login", "#login-content")
	t.Log("captured login happy matrix; rendering controlled login failures")
	invalidHTML := loginStateHTML(t, handler, "invalid")
	timeoutHandler, err := web.New(web.Config{ControlPlane: timeoutControlPlane{}, CookieSecret: []byte("0123456789abcdef0123456789abcdef"), CookieSecure: true, RequestTimeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	timeoutHTML := loginStateHTML(t, timeoutHandler, "timeout")
	if err := replaceBody(ctx, invalidHTML); err != nil {
		t.Fatalf("invalid login evidence: %v", err)
	}
	captureState(t, ctx, screenshotDir, "login-invalid", "#login-content")
	if err := replaceBody(ctx, timeoutHTML); err != nil {
		t.Fatalf("timeout login evidence: %v", err)
	}
	captureState(t, ctx, screenshotDir, "login-timeout", "#problem-content")
	if err := chromedp.Run(ctx, chromedp.Evaluate(`localStorage.removeItem('xisnove-theme');localStorage.removeItem('goshtoso-theme');localStorage.removeItem('xisnove-mode');localStorage.removeItem('goshtoso-dark')`, nil), chromedp.Navigate(ui.URL+"/login"), chromedp.WaitVisible("#email")); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(ctx,
		chromedp.SendKeys("#email", "admin@example.test"),
		chromedp.SendKeys("#password", "browser-password"),
		chromedp.Submit(`form[action="/login"]`),
		chromedp.WaitVisible("#monitor-content"),
	); err != nil {
		t.Fatalf("login: %v", err)
	}
	var loginTheme bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.documentElement.dataset.theme === 'araihu' && localStorage.getItem('xisnove-theme') === 'araihu'`, &loginTheme)); err != nil || !loginTheme {
		t.Fatalf("Arai Hû default did not survive login: applied=%t err=%v", loginTheme, err)
	}
	t.Log("awaiting Goshtoso dependency readiness before global search")
	awaitGoshtosoDependencies(t, ctx)
	t.Log("Goshtoso dependencies ready; exercising global search")
	assertGlobalSearchJourney(t, ctx, screenshotDir, monitorID.String())
	consoleMu.Lock()
	earlyConsoleProblems := append([]string(nil), consoleProblems...)
	consoleMu.Unlock()
	if len(earlyConsoleProblems) > 0 {
		t.Fatalf("browser console problems through global search: %v", earlyConsoleProblems)
	}
	var accountMenu struct {
		Expanded string   `json:"expanded"`
		Items    []string `json:"items"`
	}
	if err := chromedp.Run(ctx,
		chromedp.Click(`#account-menu > button[aria-haspopup="true"]`),
		chromedp.Poll(`document.querySelector('#account-menu [role="menu"]')?.getClientRects().length > 0`, nil, chromedp.WithPollingTimeout(5*time.Second)),
		chromedp.Evaluate(`(()=>{const menu=document.querySelector('#account-menu [role="menu"]'),items=[...menu.querySelectorAll('[role="menuitem"]')].filter(e=>e.getClientRects().length);return {expanded:document.querySelector('#account-menu > button')?.getAttribute('aria-expanded'),items:items.map(e=>e.textContent.trim())}})()`, &accountMenu),
		chromedp.KeyEvent("\u001b"),
		chromedp.Poll(`document.querySelector('#account-menu > button')?.getAttribute('aria-expanded') === 'false'`, nil),
	); err != nil {
		t.Fatalf("account menu: %v", err)
	}
	if accountMenu.Expanded != "true" || len(accountMenu.Items) != 1 || accountMenu.Items[0] != "Sign out" {
		t.Fatalf("account menu contents = %#v", accountMenu)
	}
	for _, theme := range []string{"goshtoso", "minimal", "araihu"} {
		var persisted bool
		script := fmt.Sprintf(`(()=>{document.documentElement.dataset.theme=%q;localStorage.setItem('xisnove-theme',%q);localStorage.setItem('goshtoso-theme',%q);})()`, theme, theme, theme)
		if err := chromedp.Run(ctx,
			chromedp.Evaluate(script, nil),
			chromedp.Poll(fmt.Sprintf(`localStorage.getItem('xisnove-theme')===%q`, theme), nil),
			chromedp.Reload(),
			chromedp.WaitVisible("#monitor-content"),
			chromedp.Evaluate(fmt.Sprintf(`document.documentElement.dataset.theme===%q && localStorage.getItem('xisnove-theme')===%q`, theme, theme), &persisted),
		); err != nil || !persisted {
			t.Fatalf("theme selector did not persist %s through reload: persisted=%t err=%v", theme, persisted, err)
		}
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.documentElement.classList.add('xis-visual-test')`, nil)); err != nil {
		t.Fatalf("disable theme transitions before accessibility scan: %v", err)
	}
	awaitTwoAnimationFrames(t, ctx)
	assertAccessibleSurface(t, ctx, "#monitor-content")
	assertP1Accessibility(t, ctx)
	captureMatrix(t, ctx, screenshotDir, "monitors", "#monitor-content")
	if err := chromedp.Run(ctx, chromedp.Navigate(ui.URL+"/monitors?selected="+monitorID.String()), chromedp.WaitReady("#monitor-detail"), chromedp.Sleep(1500*time.Millisecond)); err != nil {
		t.Fatalf("direct monitor detail: %v", err)
	}
	var drawerReady struct {
		Visible, Alpine, Open bool
		Display               string
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`(()=>{const owner=document.querySelector('.xis-monitor-drawer'),root=owner?.firstElementChild,panel=owner?.querySelector('aside[aria-labelledby="monitor-detail-drawerTitle"]');return {visible:!!panel&&panel.getClientRects().length>0,alpine:!!window.Alpine,open:root?._x_dataStack?.[0]?.monitorDetailDrawerIsOpen===true,display:panel?getComputedStyle(panel).display:''}})()`, &drawerReady)); err != nil || !drawerReady.Visible || !drawerReady.Alpine || !drawerReady.Open {
		t.Fatalf("direct monitor drawer did not initialize: state=%#v err=%v", drawerReady, err)
	}
	assertSelectedMonitorIdentity(t, ctx, monitorID.String())
	assertAccessibleSurface(t, ctx, "#monitor-content")
	assertDetailGeometry(t, ctx)
	assertSequentialKeyboardTraversalWithin(t, ctx, `aside[aria-labelledby="monitor-detail-drawerTitle"]`, "monitor detail drawer")
	captureMatrix(t, ctx, screenshotDir, "monitor-detail", "#monitor-detail")
	assertDrawerCloseAndRestore(t, ctx, monitorID.String())
	if err := chromedp.Run(ctx,
		chromedp.Click(`tr[data-monitor-id="`+unknownID.String()+`"] td:first-child`),
		chromedp.Poll(`new URL(location.href).searchParams.get('selected') === '`+unknownID.String()+`'`, nil),
	); err != nil {
		t.Fatalf("monitor row selection: %v", err)
	}
	assertSelectedMonitorIdentity(t, ctx, unknownID.String())
	beforeHistoryRead := monitorRequests.Load()
	homeRevision.Store(1)
	homeHealth.Store(string(sdk.Down))
	if err := chromedp.Run(ctx, chromedp.Evaluate(`history.back()`, nil), chromedp.Poll(`new URL(location.href).searchParams.has('selected') === false`, nil, chromedp.WithPollingTimeout(5*time.Second))); err != nil {
		t.Fatalf("selected monitor Back to list: %v", err)
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`history.back()`, nil), chromedp.Poll(`new URL(location.href).searchParams.get('selected') === '`+monitorID.String()+`'`, nil, chromedp.WithPollingTimeout(5*time.Second))); err != nil {
		t.Fatalf("selected monitor Back to prior detail: %v", err)
	}
	freshMarker := fmt.Sprintf(`#monitor-detail[data-monitor-state="down"][data-monitor-updated="%s"]`, observedAt.Add(time.Minute).Format(time.RFC3339))
	if err := chromedp.Run(ctx, chromedp.Poll(`!!document.querySelector(`+fmt.Sprintf("%q", freshMarker)+`) && document.querySelector('#monitor-detail')?.textContent.includes('DOWN') === true`, nil, chromedp.WithPollingTimeout(5*time.Second))); err != nil {
		var current any
		_ = chromedp.Run(ctx, chromedp.Evaluate(`(()=>{const e=document.querySelector('#monitor-detail');return {state:e?.dataset.monitorState||'',updated:e?.dataset.monitorUpdated||'',text:e?.textContent||''}})()`, &current))
		t.Fatalf("selected monitor Back retained stale state after %d reads: current=%#v err=%v", monitorRequests.Load()-beforeHistoryRead, current, err)
	}
	assertSelectedMonitorIdentity(t, ctx, monitorID.String())
	if monitorRequests.Load() <= beforeHistoryRead {
		t.Error("selected monitor Back did not re-read authoritative server state")
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`history.forward()`, nil), chromedp.Poll(`new URL(location.href).searchParams.has('selected') === false`, nil, chromedp.WithPollingTimeout(5*time.Second))); err != nil {
		t.Fatalf("selected monitor Forward to list: %v", err)
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`history.forward()`, nil), chromedp.Poll(`new URL(location.href).searchParams.get('selected') === '`+unknownID.String()+`'`, nil, chromedp.WithPollingTimeout(5*time.Second))); err != nil {
		t.Fatalf("selected monitor Forward to next detail: %v", err)
	}
	assertSelectedMonitorIdentity(t, ctx, unknownID.String())
	assertAuthoritativeRecovery(t, ctx, "status")
	assertAuthoritativeRecovery(t, ctx, "network")
	assertAuthoritativeOrdering(t, ctx)
	homeRevision.Store(0)
	homeHealth.Store(string(sdk.Up))
	if err := chromedp.Run(ctx, chromedp.Navigate(ui.URL+"/monitors"), chromedp.WaitVisible("#monitor-results")); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(ctx, chromedp.EmulateViewport(1440, 900)); err != nil {
		t.Fatal(err)
	}
	assertSequentialKeyboardTraversal(t, ctx, "desktop monitors")
	for _, state := range []struct{ name, path, ready string }{
		{"monitors-empty", "/monitors", "#monitor-results"},
		{"monitors-filtered", "/monitors?q=dns", "#monitor-results"},
		{"monitors-error", "/monitors?q=kept", "#monitor-content"},
		{"monitors-partial", "/monitors", "#monitor-results"},
	} {
		scenario.Store(state.name)
		if err := chromedp.Run(ctx, chromedp.Navigate(ui.URL+state.path), chromedp.WaitVisible(state.ready)); err != nil {
			t.Fatalf("state %s: %v", state.name, err)
		}
		captureState(t, ctx, screenshotDir, state.name, state.ready)
	}
	scenario.Store("success")
	if err := chromedp.Run(ctx, chromedp.Navigate(ui.URL+"/monitors"), chromedp.WaitVisible("#monitor-results")); err != nil {
		t.Fatal(err)
	}
	assertShellGeometry(t, ctx)
	assertMobileNavigation(t, ctx)
	var monitorHTML string
	if err := chromedp.Run(ctx, chromedp.OuterHTML("html", &monitorHTML)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(monitorHTML, "Home DNS") || strings.Contains(monitorHTML, "browser-bearer") {
		t.Fatal("HTMX result missing monitor or leaked bearer")
	}
	assertInteractiveActions(t, ctx)

	if err := chromedp.Run(ctx, chromedp.Navigate(ui.URL+"/unknown-workspace"), chromedp.WaitVisible("#problem-content")); err != nil {
		t.Fatalf("unknown route recovery: %v", err)
	}
	var inShell bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`!!document.querySelector('#main-content #problem-content a[href="/monitors"]')`, &inShell)); err != nil || !inShell {
		t.Fatalf("unknown route did not retain the authenticated shell: %v", err)
	}

	if err := chromedp.Run(ctx, chromedp.Navigate(ui.URL+"/status"), chromedp.WaitVisible("#status-content")); err != nil {
		t.Fatal(err)
	}
	assertAccessibleSurface(t, ctx, "#status-content")
	assertP1Accessibility(t, ctx)
	captureMatrix(t, ctx, screenshotDir, "status", "#status-content")
	assertSequentialKeyboardTraversal(t, ctx, "public status")
	for _, state := range []string{"public-empty", "public-unknown", "public-up", "public-degraded", "public-error", "public-timeout"} {
		scenario.Store(state)
		if err := chromedp.Run(ctx, chromedp.Navigate(ui.URL+"/status"), chromedp.WaitVisible("#status-content")); err != nil {
			t.Fatalf("state %s: %v", state, err)
		}
		if state == "public-empty" || state == "public-unknown" || state == "public-up" {
			assertCompactStatusAlert(t, ctx, state)
		}
		captureState(t, ctx, screenshotDir, state, "#status-content")
	}
	scenario.Store("success")
	if err := chromedp.Run(ctx, chromedp.Click(`a[hx-get="/status"]`), chromedp.WaitVisible("#status-content")); err != nil {
		t.Fatalf("HTMX status refresh: %v", err)
	}

	consoleMu.Lock()
	problems := append([]string(nil), consoleProblems...)
	consoleMu.Unlock()
	if len(problems) > 0 {
		t.Fatalf("browser console problems: %v", problems)
	}
	apiMu.Lock()
	calls := append([]string(nil), apiCalls...)
	apiMu.Unlock()
	for _, want := range []string{"POST /v1/sessions", "GET /v1/monitors?limit=25", "GET /v1/monitors/" + monitorID.String() + "/health", "GET /v1/status-page"} {
		if !containsCall(calls, want) {
			t.Errorf("API calls %#v missing %q", calls, want)
		}
	}
	expectedArtifacts := 216
	if os.Getenv("XISNOVE_UI_BROWSER_FAST") == "1" {
		expectedArtifacts = 17
	} else {
		expectedArtifacts = 204
	}
	assertPNGArtifacts(t, screenshotDir, expectedArtifacts)
	t.Logf("browser matrix and integrated SDK routes passed; screenshots: %s", screenshotDir)
}

func TestControlledInvalidLoginHTML(t *testing.T) {
	handler, err := web.New(web.Config{ControlPlane: controlplane.NewFake("admin@example.test", "browser-password", "token"), CookieSecret: []byte("0123456789abcdef0123456789abcdef"), CookieSecure: true, RequestTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	html := loginStateHTML(t, handler, "invalid")
	if !strings.Contains(html, "Sign-in failed") {
		t.Fatalf("invalid state missing: %s", html)
	}
}

type timeoutControlPlane struct{}

func (timeoutControlPlane) ExchangeAdministratorCredentials(ctx context.Context, _, _ string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}
func (timeoutControlPlane) RevokeSession(context.Context, string) error { return nil }
func (timeoutControlPlane) GetPublicStatusPage(context.Context) (sdk.PublicStatusPage, error) {
	return sdk.PublicStatusPage{}, nil
}
func (timeoutControlPlane) SearchResources(context.Context, string, string, int32) ([]sdk.SearchResult, error) {
	return nil, nil
}
func (timeoutControlPlane) ListMonitors(context.Context, string, string, int32) (sdk.Page[sdk.Monitor], error) {
	return sdk.Page[sdk.Monitor]{}, nil
}
func (timeoutControlPlane) GetMonitorHealth(context.Context, string, openapi_types.UUID) (sdk.MonitorHealth, error) {
	return sdk.MonitorHealth{}, nil
}

func loginStateHTML(t *testing.T, handler http.Handler, password string) string {
	t.Helper()
	getRequest := httptest.NewRequest(http.MethodGet, "https://ui.example.test/login", nil)
	getRecorder := httptest.NewRecorder()
	handler.ServeHTTP(getRecorder, getRequest)
	body := getRecorder.Body.Bytes()
	match := regexp.MustCompile(`name="_csrf" value="([^"]+)"`).FindSubmatch(body)
	if len(match) != 2 {
		t.Fatal("login csrf missing")
	}
	form := url.Values{"email": {"admin@example.test"}, "password": {password}, "_csrf": {string(match[1])}}
	postRequest := httptest.NewRequest(http.MethodPost, "https://ui.example.test/login", strings.NewReader(form.Encode()))
	postRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, cookie := range getRecorder.Result().Cookies() {
		postRequest.AddCookie(cookie)
	}
	postRecorder := httptest.NewRecorder()
	t.Logf("rendering controlled login state %q", password)
	handler.ServeHTTP(postRecorder, postRequest)
	t.Logf("rendered controlled login state %q with status %d", password, postRecorder.Code)
	return postRecorder.Body.String()
}

func replaceBody(ctx context.Context, html string) error {
	var replaced bool
	script := fmt.Sprintf(`(()=>{const next=new DOMParser().parseFromString(%q,'text/html');document.body.replaceWith(next.body);document.title=next.title;return true})()`, html)
	return chromedp.Run(ctx, chromedp.Evaluate(script, &replaced))
}

func captureState(t *testing.T, ctx context.Context, dir, name, readySelector string) {
	t.Helper()
	captureMatrix(t, ctx, dir, "state-"+name, readySelector)
}

func captureMatrix(t *testing.T, ctx context.Context, dir, surface, readySelector string) {
	t.Helper()
	awaitGoshtosoDependencies(t, ctx)
	widths, themes, modes := acceptanceAxes()
	for _, width := range widths {
		for _, theme := range themes {
			for _, mode := range modes {
				name := fmt.Sprintf("%s-%d-%s-%s.png", surface, width, theme, mode)
				t.Logf("capturing %s", name)
				var screenshot []byte
				var overflow bool
				var applied bool
				script := themeModeScript(theme, mode)
				if err := chromedp.Run(ctx,
					chromedp.EmulateViewport(width, 900),
					chromedp.Evaluate(script, nil),
					chromedp.Sleep(350*time.Millisecond),
					chromedp.Poll(fmt.Sprintf(`(()=>{const e=document.querySelector(%q);return !!e && e.getClientRects().length > 0})()`, readySelector), nil, chromedp.WithPollingTimeout(3*time.Second)),
					chromedp.Evaluate(fmt.Sprintf(`document.documentElement.dataset.theme===%q && document.documentElement.classList.contains('dark')===%t`, theme, mode == "dark"), &applied),
					chromedp.Evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth`, &overflow),
				); err != nil {
					t.Fatalf("capture %s: %v", name, err)
				}
				assertP1Accessibility(t, ctx)
				assertInteractiveActions(t, ctx)
				if err := chromedp.Run(ctx,
					chromedp.Evaluate(`(()=>{const body=document.querySelector('#monitor-detail-drawer-body');if(body)body.scrollTop=0})()`, nil),
					chromedp.Sleep(50*time.Millisecond),
					chromedp.FullScreenshot(&screenshot, 100),
				); err != nil {
					t.Fatalf("capture %s: %v", name, err)
				}
				if overflow {
					t.Errorf("%s has horizontal page overflow", name)
				}
				if !applied {
					t.Fatalf("%s theme/mode markers did not settle", name)
				}
				writePNGArtifact(t, filepath.Join(dir, name), screenshot)
			}
		}
	}
}

func awaitGoshtosoDependencies(t *testing.T, ctx context.Context) {
	t.Helper()
	var ready bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`window.goshtosoDependencies?.ready.then(()=>true)`, &ready, chromedp.EvalAsValue, func(params *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
		return params.WithAwaitPromise(true)
	})); err != nil {
		t.Fatalf("Goshtoso dependencies did not settle: %v", err)
	}
	if !ready {
		t.Fatal("Goshtoso dependencies settled without ready=true")
	}
	awaitTwoAnimationFrames(t, ctx)
}

func awaitTwoAnimationFrames(t *testing.T, ctx context.Context) {
	t.Helper()
	var settled bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve(true))))`, &settled, chromedp.EvalAsValue, func(params *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
		return params.WithAwaitPromise(true)
	})); err != nil || !settled {
		t.Fatalf("browser did not reach two-frame quiescence: settled=%t err=%v", settled, err)
	}
}

func acceptanceAxes() ([]int64, []string, []string) {
	if os.Getenv("XISNOVE_UI_BROWSER_FAST") == "1" {
		return []int64{390}, []string{"araihu"}, []string{"light"}
	}
	return []int64{390, 1440}, []string{"araihu", "goshtoso", "minimal"}, []string{"light", "dark"}
}

func themeModeScript(theme, mode string) string {
	return fmt.Sprintf(`(()=>{const root=document.documentElement,state=window.Alpine?.$data(root);root.classList.add('xis-visual-test');if(state){state.theme=%q;state.dark=%t}root.dataset.theme=%q;root.classList.toggle("dark",%t);localStorage.setItem('xisnove-theme',%q);localStorage.setItem('xisnove-mode',%q);localStorage.setItem('goshtoso-theme',%q);localStorage.setItem('goshtoso-dark',String(%t));})()`, theme, mode == "dark", theme, mode == "dark", theme, mode, theme, mode == "dark")
}

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

func writePNGArtifact(t *testing.T, path string, screenshot []byte) {
	t.Helper()
	if !bytes.HasPrefix(screenshot, pngSignature) {
		t.Fatalf("screenshot %s is not PNG encoded", filepath.Base(path))
	}
	if err := os.WriteFile(path, screenshot, 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertPNGArtifacts(t *testing.T, dir string, expected int) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.png"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != expected {
		t.Fatalf("browser run produced %d PNG artifacts, want %d", len(paths), expected)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(data, pngSignature) {
			t.Fatalf("artifact %s does not begin with the PNG signature", filepath.Base(path))
		}
	}
}

func assertCompactStatusAlert(t *testing.T, ctx context.Context, state string) {
	t.Helper()
	var geometry struct{ AlertHeight, ResultHeight, TopDelta float64 }
	if err := chromedp.Run(ctx, chromedp.Evaluate(`(()=>{const results=document.querySelector('.xis-status-results'),alert=results?.querySelector('[role="alert"]'),rr=results?.getBoundingClientRect(),ar=alert?.getBoundingClientRect();return {alertHeight:ar?.height||0,resultHeight:rr?.height||0,topDelta:Math.abs((ar?.top||0)-(rr?.top||0))}})()`, &geometry)); err != nil {
		t.Fatalf("%s status geometry: %v", state, err)
	}
	if geometry.AlertHeight <= 0 || geometry.AlertHeight >= 160 || geometry.TopDelta >= 2 || geometry.ResultHeight < geometry.AlertHeight {
		t.Fatalf("%s status alert is not compact and top-aligned: %#v", state, geometry)
	}
}

func assertAccessibleSurface(t *testing.T, ctx context.Context, selector string) {
	t.Helper()
	var result struct {
		H1      int    `json:"h1"`
		Unnamed int    `json:"unnamed"`
		Skip    string `json:"skip"`
	}
	script := fmt.Sprintf(`(() => ({h1: document.querySelectorAll(%q+" h1").length, unnamed: [...document.querySelectorAll(%q+" button,"+%q+" input,"+%q+" select")].filter(e => !e.disabled && !(e.getAttribute("aria-label") || e.labels?.length || e.textContent.trim())).length, skip: document.querySelector('a[href="#main-content"]')?.textContent.trim() || ""}))()`, selector, selector, selector, selector)
	if err := chromedp.Run(ctx, chromedp.Evaluate(script, &result)); err != nil {
		t.Fatal(err)
	}
	wantH1 := 1
	if selector == "#global-search-dialog" {
		wantH1 = 0
	}
	if result.H1 != wantH1 || result.Unnamed != 0 {
		t.Errorf("accessibility smoke for %s = %#v", selector, result)
	}
	if selector == "#monitor-content" {
		if result.Skip == "" {
			t.Error("AppShell skip link is missing")
		}
		var hasDetail bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(`!!document.querySelector('#monitor-detail')`, &hasDetail)); err != nil {
			t.Fatal(err)
		}
		if hasDetail {
			return
		}
		var focus struct {
			Href    string `json:"href"`
			Outline string `json:"outline"`
			Visible bool   `json:"visible"`
		}
		if err := chromedp.Run(ctx, chromedp.KeyEvent("\t"), chromedp.Evaluate(`(()=>{const e=document.activeElement,r=e.getBoundingClientRect();return {href:e.getAttribute("href")||"",outline:getComputedStyle(e).outlineStyle,visible:r.width>0&&r.height>0&&r.bottom>0&&r.top<innerHeight}})()`, &focus)); err != nil {
			t.Fatal(err)
		}
		if focus.Href != "#main-content" || focus.Outline == "none" || !focus.Visible {
			t.Errorf("first keyboard focus = %#v, want visible skip link", focus)
		}
	}
}

// assertP1Accessibility is the documented in-repo equivalent of an axe P1
// scan. It covers the structural failures that would block task completion and
// runs in the real browser after Goshtoso, Alpine and HTMX initialize.
func assertP1Accessibility(t *testing.T, ctx context.Context) {
	t.Helper()
	var violations []string
	script := `(() => {
		const visible = e => !!(e.offsetWidth || e.offsetHeight || e.getClientRects().length);
		const name = e => { const imageAlt = [...e.querySelectorAll('img[alt]')].map(img=>img.getAttribute('alt')).filter(Boolean).join(' '); return (e.getAttribute('aria-label') || (e.labels && [...e.labels].map(l=>l.textContent).join(' ')) || imageAlt || e.textContent || '').trim(); };
		const failures = [];
		const ids = [...document.querySelectorAll('[id]')].map(e=>e.id).filter(Boolean);
		for (const id of new Set(ids)) if (ids.filter(v=>v===id).length > 1) failures.push('duplicate-id:'+id);
		for (const e of document.querySelectorAll('button,input,select,textarea,a[href]')) if (visible(e) && !e.disabled && !name(e)) failures.push('unnamed:'+e.tagName.toLowerCase());
		for (const label of document.querySelectorAll('label[for]')) if (!document.getElementById(label.htmlFor)) failures.push('broken-label:'+label.htmlFor);
		if (document.querySelectorAll('main').length !== 1) failures.push('main-landmarks:'+document.querySelectorAll('main').length);
		if (document.querySelector('header header')) failures.push('nested-header-landmark');
		const bannerHeaders=[...document.querySelectorAll('header')].filter(e=>!e.closest('main,section,article,aside'));
		if (bannerHeaders.length > 1) failures.push('banner-landmarks:'+bannerHeaders.length);
		const colorCanvas=document.createElement('canvas'); colorCanvas.width=colorCanvas.height=1; const colorContext=colorCanvas.getContext('2d',{willReadFrequently:true});
		const parse = value => { colorContext.clearRect(0,0,1,1); colorContext.fillStyle='rgba(0,0,0,0)'; colorContext.fillStyle=value; colorContext.fillRect(0,0,1,1); const v=colorContext.getImageData(0,0,1,1).data; return [v[0],v[1],v[2],v[3]/255]; };
		const over = (fg,bg) => { const a=fg[3]+bg[3]*(1-fg[3]); if (a===0) return [0,0,0,0]; return [(fg[0]*fg[3]+bg[0]*bg[3]*(1-fg[3]))/a,(fg[1]*fg[3]+bg[1]*bg[3]*(1-fg[3]))/a,(fg[2]*fg[3]+bg[2]*bg[3]*(1-fg[3]))/a,a]; };
		const background = e => { let color=[0,0,0,0]; for(let node=e;node;node=node.parentElement) color=over(color,parse(getComputedStyle(node).backgroundColor)); return over(color,[255,255,255,1]); };
		const lum = rgb => { const c=rgb.map(v=>v/255).map(v=>v<=.03928?v/12.92:Math.pow((v+.055)/1.055,2.4)); return .2126*c[0]+.7152*c[1]+.0722*c[2]; };
		const ratio = (a,b) => (Math.max(lum(a),lum(b))+.05)/(Math.min(lum(a),lum(b))+.05);
		for (const e of document.querySelectorAll('[aria-selected]')) if (!['true','false'].includes(e.getAttribute('aria-selected'))) failures.push('aria-selected-value');
		const selected=[...document.querySelectorAll('[aria-selected="true"]')].filter(visible);
		if (selected.length > 1) failures.push('aria-selected-multiple:'+selected.length);
		for (const e of document.querySelectorAll('[aria-expanded]')) {
			if (!['true','false'].includes(e.getAttribute('aria-expanded'))) failures.push('aria-expanded-value');
			const controlled=e.getAttribute('aria-controls'); if (controlled && !document.getElementById(controlled)) failures.push('aria-controls-missing:'+controlled);
		}
		for (const e of document.querySelectorAll('[aria-current]')) if (!['page','step','location','date','time','true','false'].includes(e.getAttribute('aria-current'))) failures.push('aria-current-value');
		const detail=document.querySelector('#monitor-detail'), selectedRow=document.querySelector('tr[aria-selected="true"]');
		if (detail && selectedRow && detail.dataset.monitorId !== selectedRow.dataset.monitorId) failures.push('selection-detail-mismatch');
		if (detail && new URL(location.href).searchParams.get('selected') !== detail.dataset.monitorId) failures.push('selection-url-mismatch');
		for (const e of document.querySelectorAll('input:not([type=hidden]),select,textarea')) {
			if (!visible(e) || e.disabled) continue;
			const cs=getComputedStyle(e), fill=background(e), outside=background(e.parentElement), border=over(parse(cs.borderTopColor),outside);
			if (Math.max(ratio(fill,outside),ratio(border,outside)) < 3) failures.push('control-boundary:'+e.id);
		}
		if (innerWidth <= 390) for (const e of document.querySelectorAll('button,select,input:not([type=hidden]),textarea,.xis-native-link,.xis-action-link')) {
			if (!visible(e) || e.disabled) continue;
			const r=e.getBoundingClientRect(); if (r.width < 44 || r.height < 44) failures.push('touch-target:'+e.tagName.toLowerCase()+':'+(e.id||name(e).slice(0,24))+':'+Math.round(r.width)+'x'+Math.round(r.height)+':class='+e.className);
		}
		for (const e of document.querySelectorAll('h1,h2,h3,p,label,a,button,td,th,span,li,code,legend')) {
			if (!visible(e) || !e.textContent.trim()) continue;
			const cs=getComputedStyle(e), bg=background(e), fg=over(parse(cs.color),bg);
			const px=parseFloat(cs.fontSize), threshold=(px>=24 || (px>=18.66 && parseInt(cs.fontWeight)>=700))?3:4.5;
			const measured=ratio(fg,bg); if (measured < threshold) failures.push('contrast:'+measured.toFixed(2)+':'+e.tagName.toLowerCase()+':'+e.textContent.trim().slice(0,32)+':fg='+fg.slice(0,3).map(Math.round).join(',')+':bg='+bg.slice(0,3).map(Math.round).join(',')+':raw='+cs.color+'/'+cs.backgroundColor+':theme='+document.documentElement.dataset.theme+':dark='+document.documentElement.classList.contains('dark')+':class='+e.className+':darkMatch='+e.matches('.dark .xis-action-link'));
		}
		return [...new Set(failures)];
	})()`
	if err := chromedp.Run(ctx, chromedp.Evaluate(script, &violations)); err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		t.Fatalf("P1 accessibility violations: %v", violations)
	}
}

func assertInteractiveActions(t *testing.T, ctx context.Context) {
	t.Helper()
	var setup struct {
		Actions, Stops               int
		WindowX, WindowY, MainScroll float64
	}
	const prepare = `(()=>{const visible=e=>!!(e.offsetWidth||e.offsetHeight||e.getClientRects().length)&&getComputedStyle(e).visibility!=='hidden'&&!e.closest('[hidden],[inert],[aria-hidden="true"]'),visibleAction=e=>{if(!visible(e)||e.matches('a[href="#main-content"]'))return false;const r=e.getBoundingClientRect();return r.width>=4&&r.height>=4},modal=[...document.querySelectorAll('[aria-modal="true"]')].find(visible),root=modal||document,actions=[...root.querySelectorAll('button:not([disabled]),a[href]')].filter(visibleAction),stops=[...root.querySelectorAll('a[href],button,input:not([type=hidden]),select,textarea,[tabindex]')].filter(e=>visible(e)&&!e.disabled&&e.tabIndex>=0);actions.forEach((e,i)=>e.dataset.xisActionIndex=String(i));document.activeElement?.blur();if(modal&&stops.length){stops[0].focus()}else{document.body.setAttribute('tabindex','-1');document.body.focus();document.body.removeAttribute('tabindex')}return {actions:actions.length,stops:stops.length,windowX:scrollX,windowY:scrollY,mainScroll:document.querySelector('#main-content')?.scrollTop||0}})()`
	if err := chromedp.Run(ctx, chromedp.Evaluate(prepare, &setup)); err != nil {
		t.Fatal(err)
	}
	if setup.Actions == 0 {
		t.Fatal("rendered state has no visible enabled action to validate")
	}
	type metrics struct {
		Index                                             int
		Label, Tag, OutlineStyle, Shadow                  string
		Text, Boundary, Indicator, OutlineWidth           float64
		Hovered, BoundaryRequired, HasText, ActiveElement bool
	}
	const metricFunction = `(e=>{const canvas=document.createElement('canvas'),c=canvas.getContext('2d',{willReadFrequently:true});canvas.width=canvas.height=1;const parse=v=>{c.clearRect(0,0,1,1);c.fillStyle='rgba(0,0,0,0)';c.fillStyle=v;c.fillRect(0,0,1,1);const p=c.getImageData(0,0,1,1).data;return [p[0],p[1],p[2],p[3]/255]},over=(f,b)=>{const a=f[3]+b[3]*(1-f[3]);if(!a)return [0,0,0,0];return [(f[0]*f[3]+b[0]*b[3]*(1-f[3]))/a,(f[1]*f[3]+b[1]*b[3]*(1-f[3]))/a,(f[2]*f[3]+b[2]*b[3]*(1-f[3]))/a,a]},background=n=>{let color=[0,0,0,0];for(;n;n=n.parentElement)color=over(color,parse(getComputedStyle(n).backgroundColor));return over(color,[255,255,255,1])},lum=v=>{const q=v.slice(0,3).map(x=>x/255).map(x=>x<=.03928?x/12.92:Math.pow((x+.055)/1.055,2.4));return .2126*q[0]+.7152*q[1]+.0722*q[2]},ratio=(a,b)=>(Math.max(lum(a),lum(b))+.05)/(Math.min(lum(a),lum(b))+.05),s=getComputedStyle(e),bg=background(e),outside=background(e.parentElement),fg=over(parse(s.color),bg),borderRaw=parse(s.borderTopColor),border=over(borderRaw,outside),outline=over(parse(s.outlineColor),outside),fillBoundary=ratio(bg,outside),borderBoundary=ratio(border,outside),label=(e.getAttribute('aria-label')||e.textContent||'').trim();return {index:Number(e.dataset.xisActionIndex),label,tag:e.tagName.toLowerCase(),text:ratio(fg,bg),boundary:Math.max(fillBoundary,borderBoundary),indicator:ratio(outline,outside),outlineStyle:s.outlineStyle,outlineWidth:parseFloat(s.outlineWidth),shadow:s.boxShadow,hovered:e.matches(':hover'),boundaryRequired:parse(s.backgroundColor)[3]>0||(parseFloat(s.borderTopWidth)>0&&s.borderTopStyle!=='none'&&borderRaw[3]>0),hasText:!!e.textContent.trim(),activeElement:document.activeElement===e}})`
	validate := func(state string, value metrics) {
		threshold := 3.0
		if value.HasText {
			threshold = 4.5
		}
		if value.Text < threshold {
			t.Errorf("%s action %d %q text/icon contrast %.2f < %.1f", state, value.Index, value.Label, value.Text, threshold)
		}
		if value.BoundaryRequired && value.Boundary < 3 {
			t.Errorf("%s action %d %q boundary contrast %.2f < 3", state, value.Index, value.Label, value.Boundary)
		}
	}
	seen := make(map[int]bool, setup.Actions)
	for step := 0; step < setup.Stops; step++ {
		var index string
		if err := chromedp.Run(ctx, chromedp.KeyEvent("\t"), chromedp.Evaluate(`document.activeElement?.dataset.xisActionIndex ?? ''`, &index)); err != nil {
			t.Fatal(err)
		}
		if index == "" {
			continue
		}
		var value metrics
		if err := chromedp.Run(ctx, chromedp.Evaluate(metricFunction+`(document.activeElement)`, &value)); err != nil {
			t.Fatal(err)
		}
		seen[value.Index] = true
		validate("focus", value)
		if !value.ActiveElement || value.OutlineStyle == "none" || value.OutlineWidth < 2 || value.Indicator < 3 {
			t.Errorf("focus action %d %q has insufficient visible indicator: %#v", value.Index, value.Label, value)
		}
	}
	if len(seen) != setup.Actions {
		var diagnostic []string
		_ = chromedp.Run(ctx, chromedp.Evaluate(`(()=>{const modal=[...document.querySelectorAll('[aria-modal="true"]')].find(e=>e.getClientRects().length),root=modal||document;return [...root.querySelectorAll('button:not([disabled]),a[href],input:not([type=hidden])')].filter(e=>e.getClientRects().length).map(e=>e.tagName+':'+(e.getAttribute('aria-label')||e.textContent||e.id).trim()+':tab='+e.tabIndex+':action='+(e.dataset.xisActionIndex||''))})()`, &diagnostic))
		t.Fatalf("keyboard focus validated %d/%d visible actions: stops=%d seen=%v elements=%v", len(seen), setup.Actions, setup.Stops, seen, diagnostic)
	}
	hoveredActions := 0
	for index := 0; index < setup.Actions; index++ {
		var point struct {
			X, Y      float64
			Ready     bool
			Hoverable bool
		}
		position := fmt.Sprintf(`(()=>{const e=document.querySelector('[data-xis-action-index="%d"]');if(!e)return {hoverable:false,ready:false,x:0,y:0};const hoverable=getComputedStyle(e).pointerEvents!=='none';if(hoverable)e.scrollIntoView({block:'nearest',inline:'nearest'});const r=e.getBoundingClientRect();return {hoverable,ready:r.width>0&&r.height>0&&r.bottom>0&&r.top<innerHeight&&r.right>0&&r.left<innerWidth,x:r.left+r.width/2,y:r.top+r.height/2}})()`, index)
		if err := chromedp.Run(ctx, chromedp.Evaluate(`document.activeElement?.blur()`, nil), chromedp.Evaluate(position, &point)); err != nil {
			t.Fatalf("action %d hover geometry: %#v err=%v", index, point, err)
		}
		if !point.Hoverable {
			continue
		}
		if !point.Ready {
			t.Fatalf("action %d has no current hoverable bounding box: %#v", index, point)
		}
		hoveredActions++
		var value metrics
		measure := fmt.Sprintf(metricFunction+`(document.querySelector('[data-xis-action-index="%d"]'))`, index)
		if err := chromedp.Run(ctx, chromedp.MouseEvent(cdinput.MouseMoved, point.X, point.Y), chromedp.Sleep(20*time.Millisecond), chromedp.Evaluate(measure, &value)); err != nil {
			t.Fatal(err)
		}
		if !value.Hovered {
			t.Errorf("hover action %d %q did not match :hover after pointer move", index, value.Label)
		}
		validate("hover", value)
	}
	if hoveredActions == 0 {
		t.Fatal("rendered state has no pointer-enabled action to validate")
	}
	restore := fmt.Sprintf(`(()=>{document.querySelectorAll('[data-xis-action-index]').forEach(e=>delete e.dataset.xisActionIndex);window.scrollTo(%f,%f);const main=document.querySelector('#main-content');if(main)main.scrollTop=%f})()`, setup.WindowX, setup.WindowY, setup.MainScroll)
	if err := chromedp.Run(ctx, chromedp.MouseEvent(cdinput.MouseMoved, 0, 0), chromedp.Evaluate(restore, nil)); err != nil {
		t.Fatal(err)
	}
}

func assertSequentialKeyboardTraversal(t *testing.T, ctx context.Context, surface string) {
	assertSequentialKeyboardTraversalWithin(t, ctx, "", surface)
}

func assertGlobalSearchJourney(t *testing.T, ctx context.Context, screenshotDir, monitorID string) {
	t.Helper()
	open := func() {
		openCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := chromedp.Run(openCtx,
			chromedp.Evaluate(`window.addEventListener('keydown', event => { window.__xisLastSearchKey = {key:event.key,ctrl:event.ctrlKey,meta:event.metaKey}; }, {once:true})`, nil),
			chromedp.ActionFunc(func(actionCtx context.Context) error {
				return cdinput.DispatchKeyEvent(cdinput.KeyDown).
					WithKey("k").
					WithCode("KeyK").
					WithWindowsVirtualKeyCode(75).
					WithModifiers(cdinput.ModifierCtrl).
					Do(actionCtx)
			}),
			chromedp.ActionFunc(func(actionCtx context.Context) error {
				return cdinput.DispatchKeyEvent(cdinput.KeyUp).
					WithKey("k").
					WithCode("KeyK").
					WithWindowsVirtualKeyCode(75).
					WithModifiers(cdinput.ModifierCtrl).
					Do(actionCtx)
			}),
			chromedp.WaitVisible("#global-search-dialog"),
			chromedp.Poll(`document.activeElement?.id === 'global-search-input'`, nil),
		); err != nil {
			var diagnostic map[string]any
			_ = chromedp.Run(ctx, chromedp.Evaluate(`(()=>{const owner=document.querySelector('#global-search');return {dialogOpen:document.querySelector('#global-search-dialog')?.open===true,fieldOpen:owner?._x_dataStack?.[0]?.open===true,hasAlpine:!!window.Alpine,configured:document.querySelector('#global-search-dialog')?.dataset.xisConfigured||'',lastKey:window.__xisLastSearchKey||null}})()`, &diagnostic))
			t.Fatalf("open global search with Ctrl+K: %v diagnostic=%v", err, diagnostic)
		}
	}
	setQuery := func(query string) {
		script := fmt.Sprintf(`(()=>{const input=document.querySelector('#global-search-input');input.value=%q;input.dispatchEvent(new Event('input',{bubbles:true}))})()`, query)
		if err := chromedp.Run(ctx, chromedp.Evaluate(script, nil)); err != nil {
			t.Fatalf("set global query %q: %v", query, err)
		}
	}

	open()
	setQuery("error")
	if err := chromedp.Run(ctx, chromedp.WaitVisible(`[data-search-state="error"]`), chromedp.WaitVisible(`[data-search-retry]`)); err != nil {
		t.Fatalf("global search recovery state: %v", err)
	}
	setQuery("slow")
	if err := chromedp.Run(ctx, chromedp.Sleep(260*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	setQuery("home")
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(`#global-search-results [role="option"]`),
		chromedp.Poll(`document.querySelector('#global-search-results')?.textContent.includes('Home DNS') && !document.querySelector('#global-search-results')?.textContent.includes('VPS edge')`, nil),
	); err != nil {
		t.Fatalf("latest global search result did not win stale race: %v", err)
	}
	var active bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`(()=>{const input=document.querySelector('#global-search-input'),option=document.querySelector('#global-search-results [role="option"]');return option?.getAttribute('aria-selected')==='true'&&input?.getAttribute('aria-activedescendant')===option?.id})()`, &active)); err != nil || !active {
		t.Fatalf("global search active option contract: active=%t err=%v", active, err)
	}
	assertAccessibleSurface(t, ctx, "#global-search-dialog")
	captureMatrix(t, ctx, screenshotDir, "global-search", "#global-search-dialog")
	if err := chromedp.Run(ctx,
		chromedp.Focus("#global-search-input"),
		chromedp.KeyEvent("\r"),
		chromedp.Poll(`new URL(location.href).searchParams.get('selected') === '`+monitorID+`'`, nil),
		chromedp.WaitReady("#monitor-detail"),
	); err != nil {
		t.Fatalf("global search Enter navigation: %v", err)
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`location.assign('/monitors')`, nil), chromedp.WaitVisible("#monitor-content")); err != nil {
		t.Fatalf("return from global search destination: %v", err)
	}
	open()
	if err := chromedp.Run(ctx, chromedp.KeyEvent("\u001b"), chromedp.Poll(`document.activeElement === document.querySelector('#global-search button')`, nil)); err != nil {
		t.Fatalf("global search Escape focus return: %v", err)
	}
}

func assertSequentialKeyboardTraversalWithin(t *testing.T, ctx context.Context, selector, surface string) {
	t.Helper()
	var expected int
	rootExpression := "document"
	if selector != "" {
		rootExpression = fmt.Sprintf("document.querySelector(%q)", selector)
	}
	prepare := fmt.Sprintf(`(()=>{const root=%s,visible=e=>!!(e.offsetWidth||e.offsetHeight||e.getClientRects().length)&&getComputedStyle(e).visibility!=='hidden'&&!e.closest('[hidden],[inert],[aria-hidden="true"]');const nodes=[...root.querySelectorAll('a[href],button,input:not([type=hidden]),select,textarea,[tabindex]')].filter(e=>visible(e)&&!e.disabled&&e.tabIndex>=0);nodes.forEach((e,i)=>e.dataset.xisKeyboardIndex=String(i));document.activeElement?.blur();document.body.setAttribute('tabindex','-1');document.body.focus();document.body.removeAttribute('tabindex');return nodes.length})()`, rootExpression)
	if err := chromedp.Run(ctx, chromedp.Evaluate(prepare, &expected)); err != nil {
		t.Fatalf("prepare %s keyboard traversal: %v", surface, err)
	}
	if expected == 0 {
		t.Fatalf("%s has no visible keyboard controls", surface)
	}
	for index := 0; index < expected; index++ {
		var focus struct {
			Index        string `json:"index"`
			Outline      string `json:"outline"`
			OutlineWidth string `json:"outlineWidth"`
			Shadow       string `json:"shadow"`
		}
		if err := chromedp.Run(ctx, chromedp.KeyEvent("\t"), chromedp.Evaluate(`(()=>{const e=document.activeElement,s=getComputedStyle(e);return {index:e?.dataset.xisKeyboardIndex||'',outline:s.outlineStyle,outlineWidth:s.outlineWidth,shadow:s.boxShadow}})()`, &focus)); err != nil {
			t.Fatalf("%s keyboard step %d: %v", surface, index, err)
		}
		if focus.Index != fmt.Sprint(index) {
			t.Fatalf("%s keyboard order step %d focused index %q (hidden or out-of-order stop)", surface, index, focus.Index)
		}
		if (focus.Outline == "none" || focus.OutlineWidth == "0px") && (focus.Shadow == "none" || focus.Shadow == "") {
			t.Errorf("%s keyboard step %d has no computed visible focus indicator", surface, index)
		}
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.querySelectorAll('[data-xis-keyboard-index]').forEach(e=>delete e.dataset.xisKeyboardIndex)`, nil)); err != nil {
		t.Fatal(err)
	}
}

func assertKeyboardActivation(t *testing.T, ctx context.Context, selector, readySelector string) {
	t.Helper()
	if err := chromedp.Run(ctx, chromedp.Focus(selector), chromedp.KeyEvent("\r"), chromedp.WaitVisible(readySelector)); err != nil {
		t.Fatalf("keyboard activation %s: %v", selector, err)
	}
}

func assertShellGeometry(t *testing.T, ctx context.Context) {
	t.Helper()
	var desktop struct {
		PageOverflow bool   `json:"pageOverflow"`
		MainOverflow string `json:"mainOverflow"`
		IdleDisplay  string `json:"idleDisplay"`
		IdleHeight   int    `json:"idleHeight"`
	}
	if err := chromedp.Run(ctx, chromedp.EmulateViewport(1440, 900), chromedp.Evaluate(`(()=>{const m=document.querySelector('#main-content'),l=document.querySelector('#monitor-loading');return {pageOverflow:document.documentElement.scrollWidth>document.documentElement.clientWidth,mainOverflow:getComputedStyle(m).overflowY,idleDisplay:getComputedStyle(l).display,idleHeight:l.getBoundingClientRect().height}})()`, &desktop)); err != nil {
		t.Fatal(err)
	}
	if desktop.PageOverflow || (desktop.MainOverflow != "auto" && desktop.MainOverflow != "scroll") || desktop.IdleDisplay != "none" || desktop.IdleHeight != 0 {
		t.Errorf("desktop shell geometry = %#v", desktop)
	}
	var mobile struct {
		PageOverflow  bool `json:"pageOverflow"`
		TableOverflow bool `json:"tableOverflow"`
		RowFocus      bool `json:"rowFocus"`
		ClientWidth   int  `json:"clientWidth"`
		ScrollWidth   int  `json:"scrollWidth"`
		TableWidth    int  `json:"tableWidth"`
	}
	if err := chromedp.Run(ctx, chromedp.EmulateViewport(390, 900), chromedp.Evaluate(`(()=>{const table=document.querySelector('.xis-table-scroll table'),wrap=table?.closest('.overflow-x-auto'),row=table?.querySelector('tr[data-monitor-id]');row?.focus();return {pageOverflow:document.documentElement.scrollWidth>document.documentElement.clientWidth,tableOverflow:!!wrap&&wrap.scrollWidth>wrap.clientWidth,rowFocus:document.activeElement===row,clientWidth:wrap?.clientWidth||0,scrollWidth:wrap?.scrollWidth||0,tableWidth:Math.round(table?.getBoundingClientRect().width||0)}})()`, &mobile)); err != nil {
		t.Fatal(err)
	}
	if mobile.PageOverflow || !mobile.TableOverflow || !mobile.RowFocus {
		t.Errorf("mobile shell geometry = %#v", mobile)
	}
}

func assertDetailGeometry(t *testing.T, ctx context.Context) {
	t.Helper()
	for _, test := range []struct {
		width  int64
		mobile bool
	}{{390, true}, {1440, false}} {
		var result struct {
			PageOverflow   bool    `json:"pageOverflow"`
			HeaderBlocked  bool    `json:"headerBlocked"`
			Reachable      bool    `json:"reachable"`
			FirstReachable bool    `json:"firstReachable"`
			LastReachable  bool    `json:"lastReachable"`
			RailAfter      bool    `json:"railAfter"`
			RailBelow      bool    `json:"railBelow"`
			RailRight      bool    `json:"railRight"`
			DetailWidth    float64 `json:"detailWidth"`
			HeaderBottom   float64 `json:"headerBottom"`
			PanelTop       float64 `json:"panelTop"`
			PanelRight     float64 `json:"panelRight"`
			ViewportRight  float64 `json:"viewportRight"`
		}
		if err := chromedp.Run(ctx, chromedp.EmulateViewport(test.width, 900), chromedp.Sleep(100*time.Millisecond), chromedp.Evaluate(`(()=>{const detail=document.querySelector('#monitor-detail'),main=detail?.querySelector('.xis-detail-main'),rail=detail?.querySelector('.xis-detail-rail'),panel=document.querySelector('aside[aria-labelledby="monitor-detail-drawerTitle"]'),header=document.querySelector('.console-shell__header'),headerControl=header?.querySelector('button,select,a'),dr=detail?.getBoundingClientRect(),mr=main?.getBoundingClientRect(),rr=rail?.getBoundingClientRect(),pr=panel?.getBoundingClientRect(),hr=header?.getBoundingClientRect(),hcr=headerControl?.getBoundingClientRect(),controls=[...panel.querySelectorAll('a[href],button:not([disabled])')].filter(e=>e.getClientRects().length),first=controls[0],last=controls.at(-1),fr=first?.getBoundingClientRect();last?.scrollIntoView({block:'nearest'});const lr=last?.getBoundingClientRect(),hit=hcr?document.elementFromPoint(hcr.left+hcr.width/2,hcr.top+hcr.height/2):null;return {pageOverflow:document.documentElement.scrollWidth>document.documentElement.clientWidth,headerBlocked:!!headerControl&&hit!==headerControl&&!headerControl.contains(hit),reachable:!!pr&&pr.width>0&&pr.height>0&&pr.bottom>0&&pr.top<innerHeight,firstReachable:!!fr&&fr.top>=pr.top&&fr.bottom<=Math.min(pr.bottom,innerHeight),lastReachable:!!lr&&lr.top>=pr.top&&lr.bottom<=Math.min(pr.bottom,innerHeight),railAfter:!!main&&!!rail&&!!(main.compareDocumentPosition(rail)&Node.DOCUMENT_POSITION_FOLLOWING),railBelow:!!mr&&!!rr&&rr.top>=mr.bottom-1,railRight:!!mr&&!!rr&&rr.left>=mr.right-1,detailWidth:dr?.width||0,headerBottom:hr?.bottom||0,panelTop:pr?.top||0,panelRight:pr?.right||0,viewportRight:innerWidth}})()`, &result)); err != nil {
			t.Fatalf("detail geometry at %dpx: %v", test.width, err)
		}
		if result.PageOverflow || !result.HeaderBlocked || !result.Reachable || !result.FirstReachable || !result.LastReachable || !result.RailAfter || result.DetailWidth <= 0 || result.PanelTop+1 < result.HeaderBottom || result.PanelRight != result.ViewportRight || (test.mobile && !result.RailBelow) || (!test.mobile && !result.RailRight) {
			t.Errorf("detail geometry at %dpx = %#v", test.width, result)
		}
		if err := chromedp.Run(ctx, chromedp.Evaluate(`document.querySelector('#monitor-detail-drawer-body').scrollTop=0`, nil)); err != nil {
			t.Fatalf("reset detail drawer scroll at %dpx: %v", test.width, err)
		}
	}
}

func assertDrawerCloseAndRestore(t *testing.T, ctx context.Context, id string) {
	t.Helper()
	var retained struct {
		Open, PanelVisible bool
		Selected           string
	}
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`window.__xisCancelDrawerClose=event=>{if(event.detail?.elt?.id==='monitor-detail-close'){event.preventDefault();document.removeEventListener('htmx:beforeRequest',window.__xisCancelDrawerClose)}};document.addEventListener('htmx:beforeRequest',window.__xisCancelDrawerClose)`, nil),
		chromedp.KeyEvent("\u001b"),
		chromedp.Sleep(300*time.Millisecond),
		chromedp.Evaluate(`(()=>{const owner=document.querySelector('.xis-monitor-drawer'),root=owner?.firstElementChild,panel=owner?.querySelector('aside[aria-labelledby="monitor-detail-drawerTitle"]');return {open:root?._x_dataStack?.[0]?.monitorDetailDrawerIsOpen===true,panelVisible:!!panel&&panel.getClientRects().length>0,selected:new URL(location.href).searchParams.get('selected')||''}})()`, &retained),
	); err != nil {
		t.Fatalf("cancel monitor drawer close: %v", err)
	}
	if !retained.Open || !retained.PanelVisible || retained.Selected != id {
		t.Fatalf("cancelled drawer close lost selected state: got=%#v want=%s", retained, id)
	}
	var escapeState struct {
		Seen, Open, PanelVisible bool
		URL                      string
		Active                   string
	}
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`window.__xisEscapeSeen=0;window.addEventListener('keydown',event=>{if(event.key==='Escape')window.__xisEscapeSeen++},{once:true,capture:true})`, nil),
		chromedp.KeyEvent("\u001b"),
		chromedp.Sleep(300*time.Millisecond),
		chromedp.Evaluate(`(()=>{const owner=document.querySelector('.xis-monitor-drawer'),root=owner?.firstElementChild,panel=owner?.querySelector('aside[aria-labelledby="monitor-detail-drawerTitle"]');return {seen:window.__xisEscapeSeen>0,open:root?._x_dataStack?.[0]?.monitorDetailDrawerIsOpen===true,panelVisible:!!panel&&panel.getClientRects().length>0,url:location.href,active:document.activeElement?.outerHTML?.slice(0,160)||''}})()`, &escapeState),
	); err != nil {
		t.Fatalf("close monitor drawer with Escape: %v", err)
	}
	if !escapeState.Seen || strings.Contains(escapeState.URL, "selected=") {
		t.Fatalf("close monitor drawer with Escape did not change route: state=%#v", escapeState)
	}
	var closed struct {
		DrawerVisible bool   `json:"drawerVisible"`
		Focused       string `json:"focused"`
		SelectedCount int    `json:"selectedCount"`
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`(()=>{const panel=document.querySelector('aside[aria-labelledby="monitor-detail-drawerTitle"]'),row=document.activeElement?.closest('tr[data-monitor-id]');return {drawerVisible:!!panel&&panel.getClientRects().length>0,focused:row?.dataset.monitorId||'',selectedCount:document.querySelectorAll('tr[aria-selected="true"]').length}})()`, &closed)); err != nil {
		t.Fatal(err)
	}
	if closed.DrawerVisible || closed.Focused != id || closed.SelectedCount != 0 {
		t.Fatalf("drawer close state = %#v, want hidden drawer and focus restored to %s", closed, id)
	}
	if err := chromedp.Run(ctx, chromedp.Click(`tr[data-monitor-id="`+id+`"] td:first-child`), chromedp.Poll(`new URL(location.href).searchParams.get('selected') === '`+id+`'`, nil, chromedp.WithPollingTimeout(5*time.Second))); err != nil {
		t.Fatalf("reopen monitor drawer URL: %v", err)
	}
	if err := chromedp.Run(ctx, chromedp.Poll(`document.querySelector('#monitor-detail') !== null`, nil, chromedp.WithPollingTimeout(5*time.Second))); err != nil {
		t.Fatalf("reopen monitor drawer render: %v", err)
	}
	if err := chromedp.Run(ctx, chromedp.Poll(`document.querySelector('aside[aria-labelledby="monitor-detail-drawerTitle"]')?.getClientRects().length > 0`, nil, chromedp.WithPollingTimeout(5*time.Second))); err != nil {
		var state any
		_ = chromedp.Run(ctx, chromedp.Evaluate(`(()=>{const owner=document.querySelector('.xis-monitor-drawer'),root=owner?.firstElementChild,panel=owner?.querySelector('aside[aria-labelledby="monitor-detail-drawerTitle"]');return {url:location.href,owner:!!owner,panel:!!panel,visible:!!panel&&panel.getClientRects().length>0,open:root?._x_dataStack?.[0]?.monitorDetailDrawerIsOpen,alpine:!!window.Alpine}})()`, &state))
		t.Fatalf("reopen monitor drawer visibility: state=%#v err=%v", state, err)
	}
	assertSelectedMonitorIdentity(t, ctx, id)
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`(()=>{const body=document.body;window.__xisMonitorCloseSettled=false;window.__xisMonitorCloseXHR=null;const before=event=>{if(event.detail?.elt?.id==='monitor-detail-close'){window.__xisMonitorCloseXHR=event.detail.xhr;body.removeEventListener('htmx:beforeRequest',before)}};const after=event=>{if(window.__xisMonitorCloseXHR&&event.detail?.xhr===window.__xisMonitorCloseXHR){window.__xisMonitorCloseSettled=true;body.removeEventListener('htmx:afterSettle',after)}};body.addEventListener('htmx:beforeRequest',before);body.addEventListener('htmx:afterSettle',after)})()`, nil),
		chromedp.Click("#monitor-detail-close"),
		chromedp.Poll(`new URL(location.href).searchParams.has('selected') === false`, nil, chromedp.WithPollingTimeout(5*time.Second)),
		chromedp.Poll(`document.querySelector('aside[aria-labelledby="monitor-detail-drawerTitle"]') === null`, nil, chromedp.WithPollingTimeout(5*time.Second)),
		chromedp.Poll(`window.__xisMonitorCloseSettled === true`, nil, chromedp.WithPollingTimeout(5*time.Second)),
	); err != nil {
		t.Fatalf("close reopened monitor drawer: %v", err)
	}
}

func assertMobileNavigation(t *testing.T, ctx context.Context) {
	t.Helper()
	var opened struct {
		Expanded, Reachable, OneTrigger, FirstReachable, LastReachable bool
		Label                                                          string
		HeaderBottom, PanelTop, BackdropTop, PanelBottom               float64
	}
	var triggerFocused bool
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(390, 900),
		chromedp.Focus(`button[aria-label="Open navigation"]`),
		chromedp.Evaluate(`document.activeElement === document.querySelector('button[aria-label="Open navigation"]')`, &triggerFocused),
		chromedp.Click(`button[aria-label="Open navigation"]`),
		chromedp.Sleep(100*time.Millisecond),
		chromedp.Evaluate(`(()=>{const trigger=document.querySelector('button[aria-controls="consoleshell-sidebar"]'),panel=document.querySelector('#consoleshell-sidebar'),header=trigger?.closest('header'),backdrop=document.querySelector('.console-shell__backdrop'),r=panel?.getBoundingClientRect(),hr=header?.getBoundingClientRect(),br=backdrop?.getBoundingClientRect(),controls=[...panel.querySelectorAll('a[href],button:not([disabled])')].filter(e=>e.getClientRects().length);const first=controls[0],last=controls.at(-1),fr=first?.getBoundingClientRect();last?.scrollIntoView({block:'nearest'});const lr=last?.getBoundingClientRect();return {expanded:trigger?.getAttribute('aria-expanded')==='true',label:trigger?.getAttribute('aria-label')||'',reachable:!!r&&r.width>0&&r.height>0&&r.bottom>0&&r.top<innerHeight&&panel.querySelector('a[href="/status"]')?.textContent.includes('Public status')===true,oneTrigger:document.querySelectorAll('button[aria-controls="consoleshell-sidebar"]').length===1,firstReachable:!!fr&&fr.top>=r.top&&fr.bottom<=Math.min(r.bottom,innerHeight),lastReachable:!!lr&&lr.top>=r.top&&lr.bottom<=Math.min(r.bottom,innerHeight),headerBottom:hr?.bottom||0,panelTop:r?.top||0,backdropTop:br?.top||0,panelBottom:r?.bottom||0}})()`, &opened),
	); err != nil {
		t.Fatalf("open mobile navigation: %v", err)
	}
	if !triggerFocused {
		t.Error("mobile navigation trigger is not keyboard focusable")
	}
	if !opened.Expanded || !opened.Reachable || !opened.OneTrigger || !opened.FirstReachable || !opened.LastReachable || opened.Label != "Open navigation" || opened.HeaderBottom <= 0 || opened.PanelTop+1 < opened.HeaderBottom || opened.BackdropTop+1 < opened.HeaderBottom || opened.PanelBottom <= opened.PanelTop {
		t.Fatalf("mobile status navigation contract = %#v", opened)
	}
	assertSequentialKeyboardTraversal(t, ctx, "mobile monitors and drawer")
	var returned bool
	if err := chromedp.Run(ctx,
		chromedp.KeyEvent("\u001b"),
		chromedp.Poll(`document.querySelector('#consoleshell-sidebar')?.classList.contains('is-open') === false`, nil, chromedp.WithPollingTimeout(2*time.Second)),
		chromedp.Evaluate(`(()=>{const trigger=document.querySelector('button[aria-controls="consoleshell-sidebar"]');return document.activeElement===trigger&&trigger.getAttribute('aria-label')==='Open navigation'&&trigger.getAttribute('aria-expanded')==='false'})()`, &returned),
	); err != nil {
		t.Fatalf("close mobile navigation: %v", err)
	}
	if !returned {
		t.Error("mobile navigation did not return focus to its trigger")
	}
}

func assertSelectedMonitorIdentity(t *testing.T, ctx context.Context, id string) {
	t.Helper()
	var identity struct {
		URL, Detail, Focus, Selected string
		SelectedCount                int
	}
	pollIdentity := fmt.Sprintf(`(()=>{const selected=document.querySelector('tr[aria-selected="true"]'),result={url:new URL(location.href).searchParams.get('selected')||'',detail:document.querySelector('#monitor-detail')?.dataset.monitorId||'',focus:document.activeElement?.closest('[data-monitor-id]')?.dataset.monitorId||'',selected:selected?.dataset.monitorId||'',selectedCount:document.querySelectorAll('tr[aria-selected="true"]').length};return result.url===%[1]q&&result.detail===%[1]q&&result.focus===%[1]q&&result.selected===%[1]q&&result.selectedCount===1?result:false})()`, id)
	if err := chromedp.Run(ctx, chromedp.Poll(pollIdentity, &identity, chromedp.WithPollingTimeout(5*time.Second))); err != nil {
		var active string
		_ = chromedp.Run(ctx, chromedp.Evaluate(`document.activeElement?.outerHTML?.slice(0,240)||''`, &active))
		t.Fatalf("selected monitor focus did not settle: active=%s err=%v", active, err)
	}
	if identity.URL != id || identity.Detail != id || identity.Selected != id || identity.SelectedCount != 1 || identity.Focus != id {
		t.Fatalf("selected monitor identity = %#v, want %s", identity, id)
	}
}

func assertAuthoritativeRecovery(t *testing.T, ctx context.Context, failure string) {
	t.Helper()
	var script string
	switch failure {
	case "status":
		script = `(()=>{window.__xisRealFetch=window.fetch;window.fetch=()=>Promise.resolve(new Response("unavailable",{status:503}));window.dispatchEvent(new PageTransitionEvent("pageshow",{persisted:true}));return true})()`
	case "network":
		script = `(()=>{window.__xisRealFetch=window.fetch;window.fetch=()=>Promise.reject(new TypeError("offline"));window.dispatchEvent(new PageTransitionEvent("pageshow",{persisted:true}));return true})()`
	default:
		t.Fatalf("unsupported authoritative failure %q", failure)
	}
	var triggered bool
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(script, &triggered),
		chromedp.WaitVisible("#history-recovery"),
	); err != nil {
		t.Fatalf("%s authoritative recovery: %v", failure, err)
	}
	var recovery struct {
		OldDetail bool   `json:"oldDetail"`
		Focused   string `json:"focused"`
		Retry     bool   `json:"retry"`
		Detail    string `json:"detail"`
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`(()=>({oldDetail:!!document.querySelector('#monitor-detail'),focused:document.activeElement?.id||'',retry:!!document.querySelector('#history-recovery button'),detail:document.querySelector('#history-recovery')?.textContent||''}))()`, &recovery)); err != nil {
		t.Fatal(err)
	}
	if !triggered || recovery.OldDetail || recovery.Focused != "history-recovery-heading" || !recovery.Retry {
		t.Fatalf("%s recovery retained stale or unusable content: %#v", failure, recovery)
	}
	if (failure == "status" && !strings.Contains(recovery.Detail, "503")) || (failure == "network" && !strings.Contains(recovery.Detail, "could not reach")) {
		t.Fatalf("%s recovery copy = %q", failure, recovery.Detail)
	}
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`window.fetch=window.__xisRealFetch;delete window.__xisRealFetch`, nil),
		chromedp.Click("#history-recovery button"),
		chromedp.Poll(`document.querySelector('#monitor-detail')?.dataset.monitorId === new URL(location.href).searchParams.get('selected')`, nil, chromedp.WithPollingTimeout(5*time.Second)),
	); err != nil {
		t.Fatalf("%s authoritative retry render: %v", failure, err)
	}
	if err := chromedp.Run(ctx, chromedp.Poll(`document.querySelector('aside[aria-labelledby="monitor-detail-drawerTitle"]')?.getClientRects().length > 0`, nil, chromedp.WithPollingTimeout(5*time.Second))); err != nil {
		var state any
		_ = chromedp.Run(ctx, chromedp.Evaluate(`(()=>{const owner=document.querySelector('.xis-monitor-drawer'),root=owner?.firstElementChild,panel=owner?.querySelector('aside[aria-labelledby="monitor-detail-drawerTitle"]');return {owner:!!owner,panel:!!panel,open:root?._x_dataStack?.[0]?.monitorDetailDrawerIsOpen,visible:!!panel&&panel.getClientRects().length>0}})()`, &state))
		t.Fatalf("%s authoritative retry drawer: state=%#v err=%v", failure, state, err)
	}
}

func assertAuthoritativeOrdering(t *testing.T, ctx context.Context) {
	t.Helper()
	const install = `(()=>{window.__xisRealFetch=window.fetch;window.__xisRefreshes=[];window.fetch=(url,options)=>new Promise(resolve=>window.__xisRefreshes.push({resolve,url,signal:options?.signal}));const refresh=()=>window.dispatchEvent(new PageTransitionEvent("pageshow",{persisted:true}));refresh();refresh();return window.__xisRefreshes.length})()`
	var count int
	if err := chromedp.Run(ctx, chromedp.Evaluate(install, &count)); err != nil || count != 2 {
		t.Fatalf("install out-of-order refresh fixture: count=%d err=%v", count, err)
	}
	const newer = `<section id="ordered-newer"><h1 data-autofocus tabindex="-1">Newer authoritative state</h1></section>`
	const older = `<section id="ordered-older"><h1 data-autofocus tabindex="-1">Older stale state</h1></section>`
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(fmt.Sprintf(`window.__xisRefreshes[1].resolve(new Response(%q,{status:200}))`, newer), nil),
		chromedp.WaitVisible("#ordered-newer"),
		chromedp.Evaluate(fmt.Sprintf(`window.__xisRefreshes[0].resolve(new Response(%q,{status:200}))`, older), nil),
		chromedp.Sleep(50*time.Millisecond),
	); err != nil {
		t.Fatalf("resolve newer then older authoritative states: %v", err)
	}
	var newerOwned bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`!!document.querySelector('#ordered-newer') && !document.querySelector('#ordered-older')`, &newerOwned)); err != nil || !newerOwned {
		t.Fatalf("older response replaced newer authoritative state: owned=%t err=%v", newerOwned, err)
	}
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`window.__xisRefreshes=[];const refresh=()=>window.dispatchEvent(new PageTransitionEvent("pageshow",{persisted:true}));refresh();refresh();window.__xisRefreshes.length`, &count),
	); err != nil || count != 2 {
		t.Fatalf("install recovery ordering fixture: count=%d err=%v", count, err)
	}
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`window.__xisRefreshes[1].resolve(new Response("unavailable",{status:503}))`, nil),
		chromedp.WaitVisible("#history-recovery"),
		chromedp.Evaluate(fmt.Sprintf(`window.__xisRefreshes[0].resolve(new Response(%q,{status:200}))`, older), nil),
		chromedp.Sleep(50*time.Millisecond),
	); err != nil {
		t.Fatalf("resolve newer recovery then older state: %v", err)
	}
	var recoveryOwned bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`!!document.querySelector('#history-recovery') && !document.querySelector('#ordered-older')`, &recoveryOwned), chromedp.Evaluate(`window.fetch=window.__xisRealFetch;delete window.__xisRealFetch;delete window.__xisRefreshes`, nil)); err != nil || !recoveryOwned {
		t.Fatalf("older response replaced newer recovery: owned=%t err=%v", recoveryOwned, err)
	}
}

func requireBearer(t *testing.T, request *http.Request) {
	t.Helper()
	if request.Header.Get("Authorization") != "Bearer browser-bearer" {
		t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
	}
}

func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}

func browserBinary(t *testing.T) string {
	t.Helper()
	if configured := os.Getenv("XISNOVE_UI_BROWSER_BIN"); configured != "" {
		return configured
	}
	for _, candidate := range []string{"chromium", "chromium-browser", "google-chrome", "chrome"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	const macChrome = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	if _, err := os.Stat(macChrome); err == nil {
		return macChrome
	}
	t.Fatal("no Chromium browser found; set XISNOVE_UI_BROWSER_BIN")
	return ""
}
