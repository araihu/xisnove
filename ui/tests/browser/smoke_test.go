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
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func TestIntegratedBrowserSmoke(t *testing.T) {
	monitorID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	unknownID := uuid.MustParse("10000000-0000-4000-8000-000000000002")
	var scenario atomic.Value
	scenario.Store("success")
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
		case r.Method == http.MethodGet && r.URL.Path == "/v1/monitors":
			requireBearer(t, r)
			switch scenario.Load().(string) {
			case "monitors-loading":
				time.Sleep(300 * time.Millisecond)
				_ = json.NewEncoder(w).Encode(sdk.MonitorPage{Items: []sdk.Monitor{{Id: monitorID, Name: "Home DNS", Kind: sdk.MonitorKindDns, Enabled: true}}, Page: sdk.PageMetadata{}})
			case "monitors-empty":
				_ = json.NewEncoder(w).Encode(sdk.MonitorPage{Page: sdk.PageMetadata{}})
			case "monitors-filtered":
				_ = json.NewEncoder(w).Encode(sdk.MonitorPage{Items: []sdk.Monitor{{Id: monitorID, Name: "HTTP edge", Kind: sdk.MonitorKindHttp, Enabled: true}}, Page: sdk.PageMetadata{}})
			case "monitors-error":
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`{"title":"upstream offline"}`))
			default:
				_ = json.NewEncoder(w).Encode(sdk.MonitorPage{Items: []sdk.Monitor{{Id: monitorID, Name: "Home DNS", Description: "Resolver reachability", Kind: sdk.MonitorKindDns, Enabled: true}, {Id: unknownID, Name: "VPS edge", Description: "External ingress", Kind: sdk.MonitorKindHttp, Enabled: true}}, Page: sdk.PageMetadata{}})
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v1/monitors/"+monitorID.String()+"/health":
			requireBearer(t, r)
			if scenario.Load().(string) == "monitors-partial" {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_ = json.NewEncoder(w).Encode(sdk.MonitorHealth{MonitorId: monitorID, State: sdk.Up, LastTransitionAt: time.Now()})
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
	handler, err := web.New(web.Config{ControlPlane: adapter, CookieSecret: []byte("0123456789abcdef0123456789abcdef"), CookieSecure: true, RequestTimeout: 500 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ui := httptest.NewTLSServer(handler)
	defer ui.Close()

	browser := browserBinary(t)
	allocator, cancelAllocator := chromedp.NewExecAllocator(t.Context(), append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browser), chromedp.Flag("headless", true), chromedp.Flag("ignore-certificate-errors", true), chromedp.Flag("disable-background-networking", true), chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck)...)
	defer cancelAllocator()
	ctx, cancel := chromedp.NewContext(allocator)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, 4*time.Minute)
	defer cancelTimeout()

	var consoleMu sync.Mutex
	var consoleProblems []string
	chromedp.ListenTarget(ctx, func(event any) {
		switch value := event.(type) {
		case *cdpruntime.EventExceptionThrown:
			consoleMu.Lock()
			consoleProblems = append(consoleProblems, value.ExceptionDetails.Text)
			consoleMu.Unlock()
		case *cdpruntime.EventConsoleAPICalled:
			if value.Type == cdpruntime.APITypeError || value.Type == cdpruntime.APITypeWarning {
				consoleMu.Lock()
				consoleProblems = append(consoleProblems, string(value.Type))
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
	if err := chromedp.Run(ctx, chromedp.Navigate(ui.URL+"/login"), chromedp.WaitVisible("#email")); err != nil {
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
	assertAccessibleSurface(t, ctx, "#monitor-content")
	assertP1Accessibility(t, ctx)
	captureMatrix(t, ctx, screenshotDir, "monitors", "#monitor-content")
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
	scenario.Store("monitors-loading")
	var refreshStarted bool
	var loadingGeometry struct{ Visible, ResultsHidden, SameBounds bool }
	if err := chromedp.Run(ctx, chromedp.Evaluate(`(()=>{const e=document.querySelector('#monitor-loading'),results=document.querySelector('#monitor-results'),rr=results?.getBoundingClientRect();if(!e||!rr)return false;e.dataset.expectedLeft=String(rr.left);e.dataset.expectedWidth=String(rr.width);fetch('/monitors',{headers:{'HX-Request':'true'}}).catch(()=>{});e.classList.add('htmx-request');results.setAttribute('aria-busy','true');return true})()`, &refreshStarted), chromedp.Evaluate(`(()=>{const loading=document.querySelector('#monitor-loading'),results=document.querySelector('#monitor-results'),lr=loading?.getBoundingClientRect(),rr=results?.getBoundingClientRect();return {visible:!!loading&&getComputedStyle(loading).display!=='none'&&lr.height>0,resultsHidden:!!results&&getComputedStyle(results).display==='none'&&rr.height===0,sameBounds:!!lr&&Math.abs(lr.left-Number(loading.dataset.expectedLeft))<1&&Math.abs(lr.width-Number(loading.dataset.expectedWidth))<1}})()`, &loadingGeometry)); err != nil {
		t.Fatalf("loading state: %v", err)
	}
	if !refreshStarted {
		t.Fatal("monitor refresh link missing")
	}
	if !loadingGeometry.Visible || !loadingGeometry.ResultsHidden || !loadingGeometry.SameBounds {
		t.Fatalf("monitor loading does not replace the result surface: %#v", loadingGeometry)
	}
	captureState(t, ctx, screenshotDir, "monitors-loading", "#monitor-loading")
	if err := chromedp.Run(ctx, chromedp.Evaluate(`(()=>{const e=document.querySelector('#monitor-loading');e?.classList.remove('htmx-request');document.querySelector('#monitor-results')?.setAttribute('aria-busy','false')})()`, nil)); err != nil {
		t.Fatal(err)
	}
	scenario.Store("success")

	if err := chromedp.Run(ctx,
		chromedp.SetValue("#monitor-search", "dns"),
		chromedp.Click(`form[hx-get="/monitors"] button[type="submit"]`),
		chromedp.WaitVisible("#monitor-table"),
		chromedp.Poll(`location.search.includes("q=dns")`, nil),
	); err != nil {
		t.Fatalf("HTMX search: %v", err)
	}
	var monitorHTML string
	if err := chromedp.Run(ctx, chromedp.OuterHTML("html", &monitorHTML)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(monitorHTML, "Home DNS") || strings.Contains(monitorHTML, "browser-bearer") {
		t.Fatal("HTMX result missing monitor or leaked bearer")
	}
	var afterSwapOK bool
	if err := chromedp.Run(ctx, chromedp.Poll(`document.activeElement?.id==='main-content'`, nil), chromedp.Evaluate(`document.activeElement?.id==='main-content' && document.title==='Monitors · Xisnove'`, &afterSwapOK), chromedp.Evaluate(`(()=>{const spacer=document.createElement('div');spacer.style.height='2000px';spacer.id='history-scroll-fixture';document.querySelector('#monitor-results').append(spacer);document.querySelector('#main-content').scrollTop=200})()`, nil)); err != nil {
		t.Fatal(err)
	}
	if !afterSwapOK {
		t.Error("HTMX search did not update title and focus main content")
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`history.back()`, nil), chromedp.Poll(`location.search === ""`, nil)); err != nil {
		t.Fatalf("history back: %v", err)
	}
	var backOK bool
	if err := chromedp.Run(ctx, chromedp.Poll(`document.activeElement?.id==='main-content'`, nil), chromedp.Evaluate(`document.title==='Monitors · Xisnove' && document.querySelector('#monitor-search')?.value==='' && document.querySelector('#main-content')?.scrollTop===0`, &backOK)); err != nil {
		t.Fatal(err)
	}
	if !backOK {
		t.Error("Back did not restore monitor content, title, focus, and scroll")
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
	assertPNGArtifacts(t, screenshotDir)
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
	for _, width := range []int64{390, 1440} {
		var screenshot []byte
		var ready bool
		visibility := fmt.Sprintf(`(()=>{const e=document.querySelector(%q);return !!e&&getComputedStyle(e).display!=='none'&&getComputedStyle(e).visibility!=='hidden'&&e.getClientRects().length>0})()`, readySelector)
		if err := chromedp.Run(ctx, chromedp.EmulateViewport(width, 900), chromedp.Evaluate(`document.documentElement.classList.add('xis-visual-test')`, nil), chromedp.Sleep(350*time.Millisecond), chromedp.Evaluate(visibility, &ready)); err != nil {
			t.Fatalf("capture %s: %v", name, err)
		}
		if !ready {
			t.Fatalf("capture %s: %s is not visibly rendered", name, readySelector)
		}
		assertP1Accessibility(t, ctx)
		if err := chromedp.Run(ctx, chromedp.FullScreenshot(&screenshot, 100)); err != nil {
			t.Fatalf("capture %s: %v", name, err)
		}
		writePNGArtifact(t, filepath.Join(dir, fmt.Sprintf("state-%s-%d.png", name, width)), screenshot)
	}
}

func captureMatrix(t *testing.T, ctx context.Context, dir, surface, readySelector string) {
	t.Helper()
	widths := []int64{390, 1440}
	themes := []string{"goshtoso", "minimal"}
	modes := []string{"light", "dark"}
	if os.Getenv("XISNOVE_UI_BROWSER_FAST") == "1" {
		widths, themes, modes = []int64{390}, []string{"goshtoso"}, []string{"light"}
	}
	for _, width := range widths {
		for _, theme := range themes {
			for _, mode := range modes {
				name := fmt.Sprintf("%s-%d-%s-%s.png", surface, width, theme, mode)
				var screenshot []byte
				var overflow bool
				var applied bool
				script := fmt.Sprintf(`(()=>{const root=document.documentElement,state=window.Alpine?.$data(root);root.classList.add('xis-visual-test');if(state){state.theme=%q;state.dark=%t}root.dataset.theme=%q;root.classList.toggle("dark",%t);localStorage.setItem('xisnove-theme',%q);localStorage.setItem('xisnove-mode',%q);const themeControl=document.querySelector('#theme-choice');if(themeControl)themeControl.value=%q;const modeControl=document.querySelector('#mode-choice');if(modeControl)modeControl.value=%q;})()`, theme, mode == "dark", theme, mode == "dark", theme, mode, theme, mode)
				if err := chromedp.Run(ctx,
					chromedp.EmulateViewport(width, 900),
					chromedp.Evaluate(script, nil),
					chromedp.Sleep(350*time.Millisecond),
					chromedp.WaitVisible(readySelector),
					chromedp.Evaluate(fmt.Sprintf(`document.documentElement.dataset.theme===%q && document.documentElement.classList.contains('dark')===%t`, theme, mode == "dark"), &applied),
					chromedp.Evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth`, &overflow),
				); err != nil {
					t.Fatalf("capture %s: %v", name, err)
				}
				assertP1Accessibility(t, ctx)
				if err := chromedp.Run(ctx, chromedp.FullScreenshot(&screenshot, 100)); err != nil {
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

func assertPNGArtifacts(t *testing.T, dir string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.png"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("browser run produced no PNG artifacts")
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
	if result.H1 != 1 || result.Unnamed != 0 {
		t.Errorf("accessibility smoke for %s = %#v", selector, result)
	}
	if selector == "#monitor-content" {
		if result.Skip == "" {
			t.Error("AppShell skip link is missing")
		}
		var focus struct {
			Href    string `json:"href"`
			Outline string `json:"outline"`
		}
		if err := chromedp.Run(ctx, chromedp.Evaluate(`document.activeElement?.blur()`, nil), chromedp.KeyEvent("\t"), chromedp.Evaluate(`({href:document.activeElement.getAttribute("href")||"",outline:getComputedStyle(document.activeElement).outlineStyle})`, &focus)); err != nil {
			t.Fatal(err)
		}
		if focus.Href != "#main-content" || focus.Outline == "none" {
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
		const name = e => (e.getAttribute('aria-label') || (e.labels && [...e.labels].map(l=>l.textContent).join(' ')) || e.textContent || '').trim();
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
		const over = (fg,bg) => { const a=fg[3]+bg[3]*(1-fg[3]); if (!a) return [0,0,0,0]; return [(fg[0]*fg[3]+bg[0]*bg[3]*(1-fg[3]))/a,(fg[1]*fg[3]+bg[1]*bg[3]*(1-fg[3]))/a,(fg[2]*fg[3]+bg[2]*bg[3]*(1-fg[3]))/a,a]; };
		const background = e => { let color=[0,0,0,0]; for(let node=e;node;node=node.parentElement) color=over(color,parse(getComputedStyle(node).backgroundColor)); return over(color,[255,255,255,1]); };
		const lum = rgb => { const c=rgb.map(v=>v/255).map(v=>v<=.03928?v/12.92:Math.pow((v+.055)/1.055,2.4)); return .2126*c[0]+.7152*c[1]+.0722*c[2]; };
		const ratio = (a,b) => (Math.max(lum(a),lum(b))+.05)/(Math.min(lum(a),lum(b))+.05);
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

func assertSequentialKeyboardTraversal(t *testing.T, ctx context.Context, surface string) {
	t.Helper()
	var expected int
	const prepare = `(()=>{const visible=e=>!!(e.offsetWidth||e.offsetHeight||e.getClientRects().length)&&getComputedStyle(e).visibility!=='hidden'&&!e.closest('[hidden],[inert],[aria-hidden="true"]');const nodes=[...document.querySelectorAll('a[href],button,input:not([type=hidden]),select,textarea,[tabindex]')].filter(e=>visible(e)&&!e.disabled&&e.tabIndex>=0);nodes.forEach((e,i)=>e.dataset.xisKeyboardIndex=String(i));document.activeElement?.blur();document.body.setAttribute('tabindex','-1');document.body.focus();document.body.removeAttribute('tabindex');return nodes.length})()`
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
		PageOverflow    bool   `json:"pageOverflow"`
		MainOverflow    string `json:"mainOverflow"`
		IdleDisplay     string `json:"idleDisplay"`
		IdleHeight      int    `json:"idleHeight"`
		PlaceholderFits bool   `json:"placeholderFits"`
	}
	if err := chromedp.Run(ctx, chromedp.EmulateViewport(1440, 900), chromedp.Evaluate(`(()=>{const m=document.querySelector('#main-content'),l=document.querySelector('#monitor-loading'),input=document.querySelector('#monitor-search'),canvas=document.createElement('canvas'),c=canvas.getContext('2d'),style=getComputedStyle(input);c.font=style.font;const needed=c.measureText(input.placeholder).width+parseFloat(style.paddingLeft)+parseFloat(style.paddingRight)+8;return {pageOverflow:document.documentElement.scrollWidth>document.documentElement.clientWidth,mainOverflow:getComputedStyle(m).overflowY,idleDisplay:getComputedStyle(l).display,idleHeight:l.getBoundingClientRect().height,placeholderFits:needed<=input.clientWidth}})()`, &desktop)); err != nil {
		t.Fatal(err)
	}
	if desktop.PageOverflow || (desktop.MainOverflow != "auto" && desktop.MainOverflow != "scroll") || desktop.IdleDisplay != "none" || desktop.IdleHeight != 0 || !desktop.PlaceholderFits {
		t.Errorf("desktop shell geometry = %#v", desktop)
	}
	var mobile struct {
		PageOverflow  bool `json:"pageOverflow"`
		TableOverflow bool `json:"tableOverflow"`
		ActionFocus   bool `json:"actionFocus"`
		ClientWidth   int  `json:"clientWidth"`
		ScrollWidth   int  `json:"scrollWidth"`
		TableWidth    int  `json:"tableWidth"`
	}
	if err := chromedp.Run(ctx, chromedp.EmulateViewport(390, 900), chromedp.Evaluate(`(()=>{const table=document.querySelector('.xis-table-scroll table'),wrap=table?.closest('.overflow-x-auto');const action=document.querySelector('[aria-label^="Select monitor"]');action?.focus();return {pageOverflow:document.documentElement.scrollWidth>document.documentElement.clientWidth,tableOverflow:!!wrap&&wrap.scrollWidth>wrap.clientWidth,actionFocus:document.activeElement===action,clientWidth:wrap?.clientWidth||0,scrollWidth:wrap?.scrollWidth||0,tableWidth:Math.round(table?.getBoundingClientRect().width||0)}})()`, &mobile)); err != nil {
		t.Fatal(err)
	}
	if mobile.PageOverflow || !mobile.TableOverflow || !mobile.ActionFocus {
		t.Errorf("mobile shell geometry = %#v", mobile)
	}
}

func assertMobileNavigation(t *testing.T, ctx context.Context) {
	t.Helper()
	var opened bool
	var triggerFocused bool
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(390, 900),
		chromedp.Focus(`button[aria-label="Open monitoring navigation"]`),
		chromedp.Evaluate(`document.activeElement === document.querySelector('button[aria-label="Open monitoring navigation"]')`, &triggerFocused),
		chromedp.Click(`button[aria-label="Open monitoring navigation"]`),
		chromedp.Sleep(100*time.Millisecond),
		chromedp.Evaluate(`(()=>{const trigger=document.querySelector('button[aria-label="Open monitoring navigation"]'),panel=document.querySelector('#mobile-monitoring-panel');return trigger?.getAttribute('aria-expanded')==='true' && getComputedStyle(panel).display!=='none' && panel.querySelector('a[href="/status"]')?.textContent.includes('Public status')===true})()`, &opened),
	); err != nil {
		t.Fatalf("open mobile navigation: %v", err)
	}
	if !triggerFocused {
		t.Error("mobile navigation trigger is not keyboard focusable")
	}
	if !opened {
		t.Fatal("mobile status navigation did not open with its public trigger")
	}
	assertSequentialKeyboardTraversal(t, ctx, "mobile monitors and drawer")
	var returned bool
	if err := chromedp.Run(ctx,
		chromedp.KeyEvent("\u001b"),
		chromedp.WaitNotVisible("#mobile-monitoring-panel"),
		chromedp.Evaluate(`document.activeElement === document.querySelector('button[aria-label="Open monitoring navigation"]')`, &returned),
	); err != nil {
		t.Fatalf("close mobile navigation: %v", err)
	}
	if !returned {
		t.Error("mobile navigation did not return focus to its trigger")
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
