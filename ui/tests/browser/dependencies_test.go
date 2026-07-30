//go:build browser

package browser_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/araihu/goshtoso/assets"
	"github.com/araihu/goshtoso/components/head"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

func TestDefaultDependenciesCDNFailureUsesOrderedEmbeddedFallback(t *testing.T) {
	server := dependencyProbeServer(t, false)
	ctx, cancel := dependencyBrowser(t)
	defer cancel()
	installDependencyObserver(t, ctx)
	if err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL),
		chromedp.Poll(`window.__xisDependencies?.ready === true`, nil),
	); err != nil {
		t.Fatalf("wait for default dependency fallback: %v", err)
	}

	var result struct {
		Fallbacks []string          `json:"fallbacks"`
		Sources   map[string]string `json:"sources"`
		Ready     int               `json:"ready"`
		Errors    int               `json:"errors"`
		Nonces    []string          `json:"nonces"`
		SRI       bool              `json:"sri"`
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`(()=>({
		fallbacks: window.__xisDependencies.fallbacks,
		sources: window.goshtosoDependencies.sources,
		ready: window.__xisDependencies.readyEvents,
		errors: window.__xisDependencies.errors,
		nonces: [...document.querySelectorAll('script[data-goshtoso-dependency]')].map(script => script.nonce),
		sri: [...document.querySelectorAll('script[data-goshtoso-dependency]:not([data-goshtoso-dependency="combobox"])')].every(script => script.integrity.startsWith('sha384-')),
	}))()`, &result)); err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{"alpine-collapse", "alpine-focus", "alpine-mask", "alpine", "htmx"}
	if fmt.Sprint(result.Fallbacks) != fmt.Sprint(wantOrder) {
		t.Fatalf("fallback order = %v, want %v", result.Fallbacks, wantOrder)
	}
	for _, name := range wantOrder {
		if result.Sources[name] != "fallback" {
			t.Errorf("%s source = %q, want fallback", name, result.Sources[name])
		}
	}
	if result.Sources["combobox"] != "primary" || result.Ready != 1 || result.Errors != 0 || !result.SRI {
		t.Fatalf("dependency ledger = %#v", result)
	}
	if len(result.Nonces) != 6 {
		t.Fatalf("dynamic script nonce count = %d, want 6", len(result.Nonces))
	}
	for _, nonce := range result.Nonces {
		if nonce != "xisnove-probe-nonce" {
			t.Errorf("dynamic script nonce = %q", nonce)
		}
	}

	if err := chromedp.Run(ctx,
		chromedp.Click("#alpine-toggle"), chromedp.WaitVisible("#collapse-panel"),
		chromedp.SendKeys("#mask-input", "123456"),
		chromedp.Click("#trap-toggle"), chromedp.Poll(`document.activeElement?.id === 'trapped-input'`, nil),
		chromedp.Click("#trap-close"),
		chromedp.Click("#htmx-trigger"), chromedp.WaitVisible("#htmx-result"),
		chromedp.Focus("#combobox"), chromedp.KeyEvent(kb.ArrowDown),
	); err != nil {
		t.Fatalf("fallback runtime behavior: %v", err)
	}
	var behavior struct{ Mask, Focus string }
	if err := chromedp.Run(ctx, chromedp.Evaluate(`({mask:document.querySelector('#mask-input').value,focus:document.activeElement?.id||''})`, &behavior)); err != nil {
		t.Fatal(err)
	}
	if behavior.Mask != "123-456" || behavior.Focus != "combobox-first" {
		t.Fatalf("fallback behaviors = %#v", behavior)
	}
}

func TestDependencyTerminalFailureEmitsErrorAndRejectsReadyWithoutUnhandledException(t *testing.T) {
	server := dependencyProbeServer(t, true)
	ctx, cancel := dependencyBrowser(t)
	defer cancel()
	installDependencyObserver(t, ctx)
	if err := chromedp.Run(ctx, chromedp.Navigate(server.URL), chromedp.Poll(`window.__xisDependencies?.caught === true`, nil)); err != nil {
		t.Fatalf("wait for terminal dependency failure: %v", err)
	}
	var result struct {
		Fallbacks []string `json:"fallbacks"`
		Ready     int      `json:"ready"`
		Errors    int      `json:"errors"`
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`window.__xisDependencies`, &result)); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(result.Fallbacks) != "[alpine-mask]" || result.Ready != 0 || result.Errors != 1 {
		t.Fatalf("terminal dependency events = %#v", result)
	}
}

func dependencyProbeServer(t *testing.T, terminal bool) *httptest.Server {
	t.Helper()
	options := []head.Option{}
	if terminal {
		options = []head.Option{
			head.WithDependencyCDNURL(head.DependencyAlpineCollapse, assets.AlpineCollapseURL),
			head.WithDependencyCDNURL(head.DependencyAlpineFocus, assets.AlpineFocusURL),
			head.WithDependencyCDNURL(head.DependencyAlpineMask, "/cdn-unavailable/alpine-mask.js"),
			head.WithDependencyLocalURL(head.DependencyAlpineMask, "/local-unavailable/alpine-mask.js"),
		}
	}
	ctx := templ.WithNonce(context.Background(), "xisnove-probe-nonce")
	var dependencies strings.Builder
	if err := head.Dependencies(options...).Render(ctx, &dependencies); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /assets/", assets.Handler())
	mux.HandleFunc("GET /cdn-unavailable/", unavailableScript)
	mux.HandleFunc("GET /local-unavailable/", unavailableScript)
	mux.HandleFunc("GET /fragment", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `<strong id="htmx-result">HTMX works</strong>`)
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'nonce-xisnove-probe-nonce' 'strict-dynamic' 'unsafe-eval' https://unpkg.com 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'")
		_, _ = fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8">%s</head><body>
<div id="alpine-fixture" x-data="{open:false}"><button id="alpine-toggle" x-on:click="open=!open">Toggle</button><div id="collapse-panel" x-show="open" x-collapse>Collapse</div></div>
<div x-data><label for="mask-input">Masked value</label><input id="mask-input" x-mask="999-999"></div>
<div x-data="{trapped:false}"><button id="trap-toggle" x-on:click="trapped=true">Trap</button><div x-show="trapped" x-trap="trapped"><input id="trapped-input"><button id="trap-close" x-on:click="trapped=false">Close</button></div></div>
<button id="htmx-trigger" hx-get="/fragment" hx-target="#htmx-target">Load</button><div id="htmx-target"></div>
<div id="combobox" data-combobox tabindex="0"><button id="combobox-first" role="option" tabindex="-1">First</button><button role="option" tabindex="-1">Second</button></div>
</body></html>`, dependencies.String())
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func unavailableScript(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "simulated outage", http.StatusServiceUnavailable)
}

func dependencyBrowser(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	allocator, cancelAllocator := chromedp.NewExecAllocator(t.Context(), append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browserBinary(t)), chromedp.Flag("headless", true), chromedp.Flag("disable-background-networking", true), chromedp.Flag("host-resolver-rules", "MAP unpkg.com 127.0.0.1"), chromedp.NoSandbox, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck)...)
	ctx, cancelBrowser := chromedp.NewContext(allocator)
	ctx, cancelTimeout := context.WithTimeout(ctx, 90*time.Second)
	return ctx, func() { cancelTimeout(); cancelBrowser(); cancelAllocator() }
}

func installDependencyObserver(t *testing.T, ctx context.Context) {
	t.Helper()
	script := `window.__xisDependencies={fallbacks:[],readyEvents:0,errors:0,caught:false};
window.addEventListener('goshtoso:dependency-fallback',event=>window.__xisDependencies.fallbacks.push(event.detail.dependency));
window.addEventListener('goshtoso:dependencies-ready',()=>window.__xisDependencies.readyEvents++);
window.addEventListener('goshtoso:dependency-error',()=>window.__xisDependencies.errors++);
document.addEventListener('DOMContentLoaded',()=>window.goshtosoDependencies.ready.then(()=>window.__xisDependencies.ready=true).catch(()=>window.__xisDependencies.caught=true));`
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument(script).Do(ctx)
		return err
	})); err != nil {
		t.Fatal(err)
	}
}
