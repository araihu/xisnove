//go:build browser

package browser_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/araihu/xisnove/sdk"
	"github.com/araihu/xisnove/ui/internal/controlplane"
	"github.com/araihu/xisnove/ui/internal/web"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/google/uuid"
)

func TestIntegratedBrowserSmoke(t *testing.T) {
	monitorID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	apiCalls := make([]string, 0, 8)
	var apiMu sync.Mutex
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiMu.Lock()
		apiCalls = append(apiCalls, r.Method+" "+r.URL.RequestURI())
		apiMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sessions":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(sdk.Session{Token: "browser-bearer", ExpiresAt: time.Now().Add(time.Hour)})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/sessions/current":
			requireBearer(t, r)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/status-page":
			_ = json.NewEncoder(w).Encode(sdk.PublicStatusPage{GeneratedAt: time.Now(), State: sdk.Degraded, Monitors: []sdk.PublicStatusMonitor{{Id: monitorID, Name: "Home DNS", Description: "Resolver reachability", State: sdk.Up}}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/monitors":
			requireBearer(t, r)
			_ = json.NewEncoder(w).Encode(sdk.MonitorPage{Items: []sdk.Monitor{{Id: monitorID, Name: "Home DNS", Description: "Resolver reachability", Kind: sdk.MonitorKindDns, Enabled: true}}, Page: sdk.PageMetadata{}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/monitors/"+monitorID.String()+"/health":
			requireBearer(t, r)
			_ = json.NewEncoder(w).Encode(sdk.MonitorHealth{MonitorId: monitorID, State: sdk.Up, LastTransitionAt: time.Now()})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	adapter, err := controlplane.NewSDKClient(api.URL, api.Client())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := web.New(web.Config{ControlPlane: adapter, CookieSecret: []byte("0123456789abcdef0123456789abcdef"), CookieSecure: true, RequestTimeout: 5 * time.Second})
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
	ctx, cancelTimeout := context.WithTimeout(ctx, 2*time.Minute)
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
	captureMatrix(t, ctx, screenshotDir, "login", "#login-content")
	if err := chromedp.Run(ctx,
		chromedp.SendKeys("#email", "admin@example.test"),
		chromedp.SendKeys("#password", "browser-password"),
		chromedp.Submit(`form[action="/login"]`),
		chromedp.WaitVisible("#monitor-content"),
	); err != nil {
		t.Fatalf("login: %v", err)
	}
	assertAccessibleSurface(t, ctx, "#monitor-content")
	captureMatrix(t, ctx, screenshotDir, "monitors", "#monitor-content")

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
	if err := chromedp.Run(ctx, chromedp.Evaluate(`history.back()`, nil), chromedp.Poll(`location.search === ""`, nil)); err != nil {
		t.Fatalf("history back: %v", err)
	}

	if err := chromedp.Run(ctx, chromedp.Navigate(ui.URL+"/status"), chromedp.WaitVisible("#status-content")); err != nil {
		t.Fatal(err)
	}
	assertAccessibleSurface(t, ctx, "#status-content")
	captureMatrix(t, ctx, screenshotDir, "status", "#status-content")
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
	t.Logf("browser matrix and integrated SDK routes passed; screenshots: %s", screenshotDir)
}

func captureMatrix(t *testing.T, ctx context.Context, dir, surface, readySelector string) {
	t.Helper()
	for _, width := range []int64{390, 1440} {
		for _, theme := range []string{"goshtoso", "minimal"} {
			for _, mode := range []string{"light", "dark"} {
				name := fmt.Sprintf("%s-%d-%s-%s.png", surface, width, theme, mode)
				var screenshot []byte
				var overflow bool
				script := fmt.Sprintf(`(()=>{document.documentElement.dataset.theme=%q;document.documentElement.classList.toggle("dark",%t);const themeControl=document.querySelector('#theme-choice');if(themeControl)themeControl.value=%q;const modeControl=document.querySelector('#mode-choice');if(modeControl)modeControl.value=%q;})()`, theme, mode == "dark", theme, mode)
				if err := chromedp.Run(ctx,
					chromedp.EmulateViewport(width, 900),
					chromedp.Evaluate(script, nil),
					chromedp.WaitVisible(readySelector),
					chromedp.Evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth`, &overflow),
					chromedp.FullScreenshot(&screenshot, 90),
				); err != nil {
					t.Fatalf("capture %s: %v", name, err)
				}
				if overflow {
					t.Errorf("%s has horizontal page overflow", name)
				}
				if err := os.WriteFile(filepath.Join(dir, name), screenshot, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
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
