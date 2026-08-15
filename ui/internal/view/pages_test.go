package view

import (
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/araihu/xisnove/sdk"
	"github.com/araihu/xisnove/ui/internal/seasonalassets"
	"github.com/google/uuid"
)

func TestBrandUsesCanonicalV10IdentityAssets(t *testing.T) {
	var rendered strings.Builder
	if err := Brand().Render(t.Context(), &rendered); err != nil {
		t.Fatalf("render brand: %v", err)
	}
	body := rendered.String()
	for _, want := range []string{`aria-label="Xisnove"`, `/ui/xisnove-logo-ab01f1a.svg`, `/ui/xisnove-mark-ab01f1a.svg`, `/ui/xisnove-mark-reverse-ab01f1a.svg`} {
		if !strings.Contains(body, want) {
			t.Fatalf("canonical brand missing %q: %s", want, body)
		}
	}
}

func TestDocumentLoadsAraiHuThemeAfterGoshtosoAndUsesItByDefault(t *testing.T) {
	var rendered strings.Builder
	if err := Document("Theme contract", Brand()).Render(t.Context(), &rendered); err != nil {
		t.Fatalf("render document: %v", err)
	}
	body := rendered.String()
	if !strings.Contains(body, `<link rel="icon" type="image/svg+xml" href="/ui/xisnove-ab01f1a.svg">`) {
		t.Fatalf("document does not reference the canonical versioned Xisnove favicon: %s", body)
	}
	goshtoso := strings.Index(body, `/assets/styles.css`)
	araihu := strings.Index(body, `/ui/araihu-v0.2.1.css`)
	if goshtoso < 0 || araihu < 0 || araihu <= goshtoso {
		t.Fatalf("Arai Hû stylesheet must follow Goshtoso: %s", body)
	}
	for _, want := range []string{
		`data-theme="araihu"`,
		`localStorage.getItem('xisnove-theme') || 'araihu'`,
		`localStorage.setItem('xisnove-theme', theme)`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("theme document contract missing %q", want)
		}
	}

}

func TestHeaderControlsKeepSearchShortcutAndCircleAccountTrigger(t *testing.T) {
	var rendered strings.Builder
	if err := Document("Header contract", Brand()).Render(t.Context(), &rendered); err != nil {
		t.Fatalf("render document: %v", err)
	}
	body := rendered.String()
	for _, want := range []string{
		`.xis-global-search-trigger kbd {`,
		`.xis-account-menu > div > button[aria-haspopup="true"]`,
		`border-radius: 9999px;`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("header stylesheet missing %q", want)
		}
	}
	if strings.Contains(body, `.xis-global-search-trigger kbd { display: none; }`) {
		t.Fatal("mobile header hides the search shortcut")
	}
}

func TestConsolePageUsesGoshtosoAppShellWithXisnoveSlots(t *testing.T) {
	var rendered strings.Builder
	if err := ConsolePage("Monitors", "csrf-token", MonitorContent(MonitorList{})).Render(t.Context(), &rendered); err != nil {
		t.Fatalf("render console page: %v", err)
	}
	body := rendered.String()
	for _, want := range []string{
		`class="console-shell-root"`,
		`/consoleshell/assets/shell.css`,
		`/consoleshell/assets/shell.js`,
		`/assets/styles.css`,
		`/ui/araihu-v0.2.1.css`,
		seasonalassets.LogoPath,
		seasonalassets.FaviconPath,
		`/assets/icons/heroicons.svg#hi-16-solid-magnifying-glass`,
		`data-consoleshell-nav-id="nav-monitors"`,
		`id="global-search-dialog"`,
		`xis-search-trigger-button`,
		`class="xis-dark-mode-toggle"`,
		`class="xis-console-sidebar-header"`,
		`class="xis-global-search-close"`,
		`src="/ui/app.js"`,
		`id="main-content"`,
		`href="/status"`,
		`id="account-menu"`,
		`Sign out`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("console shell missing %q", want)
		}
	}
	if strings.Contains(body, "mobile-monitoring-panel") || strings.Contains(body, "xis-mobile-nav-backdrop") {
		t.Fatal("console shell retained the superseded application-owned mobile navigation")
	}
	for _, absent := range []string{`id="theme-choice"`, `id="mode-choice"`, `Monitor tools`, `id="monitor-search"`, "bounded control-plane requests"} {
		if strings.Contains(body, absent) {
			t.Errorf("console shell retained removed monitor control %q", absent)
		}
	}
}

func TestConsoleFragmentUsesAppShellMainWithoutASecondDocument(t *testing.T) {
	var rendered strings.Builder
	if err := ConsoleFragment("Monitors", "csrf-token", MonitorContent(MonitorList{})).Render(t.Context(), &rendered); err != nil {
		t.Fatalf("render console fragment: %v", err)
	}
	body := rendered.String()
	if strings.Contains(body, "<html") {
		t.Fatalf("console fragment rendered a full document: %s", body)
	}
	for _, want := range []string{`<main id="main-content"`, `id="console-content"`, `id="monitor-content"`} {
		if !strings.Contains(body, want) {
			t.Errorf("console fragment missing %q", want)
		}
	}
	if strings.Contains(body, "hx-swap-oob") {
		t.Fatal("console fragment emitted an OOB sidebar update without a route that changes navigation")
	}
}

func TestLoginContentNamesItsMainRegionAndKeepsPasswordSafeWithoutJavaScript(t *testing.T) {
	var rendered strings.Builder
	if err := LoginContent("csrf-token", "").Render(t.Context(), &rendered); err != nil {
		t.Fatalf("render login content: %v", err)
	}
	body := rendered.String()
	for _, want := range []string{`aria-labelledby="login-heading"`, `id="login-heading"`, `type="password"`} {
		if !strings.Contains(body, want) {
			t.Errorf("login content missing %q", want)
		}
	}
}

func TestStatusContentAnnouncesHTMXRefreshes(t *testing.T) {
	var rendered strings.Builder
	if err := StatusContent(sdk.PublicStatusPage{State: sdk.Unknown}).Render(t.Context(), &rendered); err != nil {
		t.Fatalf("render status content: %v", err)
	}
	body := rendered.String()
	if !strings.Contains(body, `aria-live="polite"`) || !strings.Contains(body, `class="xis-status-results xis-stack"`) {
		t.Fatalf("status content is not a polite live region: %s", rendered.String())
	}
	if strings.Contains(body, `class="xis-status-results xis-results-surface`) {
		t.Fatalf("status results must not inherit the monitor result minimum height: %s", body)
	}
}

func TestLoadingSurfacesPrecedeAndReplaceResults(t *testing.T) {
	var rendered strings.Builder
	if err := MonitorContent(MonitorList{}).Render(t.Context(), &rendered); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	loading := strings.Index(body, `id="monitor-loading"`)
	results := strings.Index(body, `id="monitor-results"`)
	if loading < 0 || results < 0 || loading >= results {
		t.Fatalf("loading/result sibling order is not stable: %s", body)
	}
	for _, want := range []string{"xis-results-surface", "xis-results"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, absent := range []string{`role="toolbar"`, `id="monitor-search"`, "Monitor tools", "bounded control-plane requests"} {
		if strings.Contains(body, absent) {
			t.Errorf("monitor content retained removed control %q", absent)
		}
	}
}

func TestMonitorContentDistinguishesEmptyFilteredAndPartialStates(t *testing.T) {
	for _, test := range []struct {
		name string
		data MonitorList
		want []string
	}{
		{name: "empty", data: MonitorList{}, want: []string{"No monitors yet", `id="monitor-loading"`}},
		{name: "filtered", data: MonitorList{Query: "dns"}, want: []string{"No matching monitors", "Clear search"}},
		{name: "partial", data: MonitorList{HealthFailures: 1}, want: []string{"Some health is unavailable", "No monitors yet"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var rendered strings.Builder
			if err := MonitorContent(test.data).Render(t.Context(), &rendered); err != nil {
				t.Fatal(err)
			}
			for _, want := range test.want {
				if !strings.Contains(rendered.String(), want) {
					t.Errorf("missing %q", want)
				}
			}
		})
	}
}

func TestMonitorContentRendersSelectedMonitorDetailWorkspace(t *testing.T) {
	monitorID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	locationID := uuid.MustParse("20000000-0000-4000-8000-000000000001")
	observedAt := time.Date(2026, time.July, 26, 12, 30, 0, 0, time.UTC)
	data := MonitorList{
		Monitors: []sdk.Monitor{{
			Id: monitorID, Name: "Home DNS", Description: "Resolver reachability",
			Kind: sdk.MonitorKindDns, Enabled: true, Public: true,
			LocationId: locationID, RequiredLocation: true,
			IntervalSeconds: 60, TimeoutMillis: 2500,
			FailureThreshold: 3, RecoveryThreshold: 2, UpdatedAt: observedAt,
		}},
		Health: map[string]sdk.MonitorHealth{
			monitorID.String(): {MonitorId: monitorID, State: sdk.Degraded, LastTransitionAt: observedAt},
		},
		Query: "dns", Cursor: "opaque/page", Selected: monitorID.String(),
	}

	var rendered strings.Builder
	if err := MonitorContent(data).Render(t.Context(), &rendered); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	for _, want := range []string{
		`id="monitor-detail-drawer-body"`, `role="dialog"`, `aria-labelledby="monitor-detail-drawerTitle"`,
		`x-trap.noscroll="monitorDetailDrawerIsOpen"`, `xis-monitor-drawer-panel`,
		`data-monitor-drawer-close-url="/monitors?cursor=opaque%2Fpage&amp;q=dns"`,
		`id="monitor-detail"`, `aria-labelledby="monitor-detail-drawerTitle"`,
		`data-monitor-id="` + monitorID.String() + `"`, `hx-get="/monitors?cursor=opaque%2Fpage&amp;q=dns&amp;selected=` + monitorID.String() + `"`, `hx-target="#main-content"`, `hx-swap="outerHTML"`, `hx-push-url="true"`, `role="button"`, `tabindex="0"`, `aria-selected="true"`,
		`id="monitor-detail-drawerTitle"`, `Home DNS`, `data-monitor-state="DEGRADED"`, `id="monitor-detail-heading" class="xis-detail-section-heading" tabindex="-1" data-autofocus`, "Home DNS", "DEGRADED",
		"Current health", "Configuration", "Live availability",
		`data-goshtoso-charts-live-event="chart"`,
		`data-goshtoso-charts-live-url="/monitors/` + monitorID.String() + `/availability/events"`,
		"Last 3 hours.", "60 seconds", "2500 ms", "3 failures", "2 successes",
		"26 Jul 2026 12:30 UTC",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("selected monitor workspace missing %q", want)
		}
	}
	for _, absent := range []string{`aria-label="Select monitor `, `>Select</a>`} {
		if strings.Contains(body, absent) {
			t.Errorf("selected monitor workspace retained removed row action/detail %q", absent)
		}
	}
	for _, absent := range []string{
		`<p class="xis-meta"><code>` + monitorID.String() + `</code></p>`,
		`<dt>Location</dt><dd><code>` + locationID.String() + `</code></dd>`,
		`Selected monitor`, `id="monitor-detail-close"`, "Each bar is one probe sample.",
	} {
		if strings.Contains(body, absent) {
			t.Errorf("selected monitor workspace retained technical identifier %q", absent)
		}
	}
	if strings.Count(body, "<h1") != 1 {
		t.Errorf("selected monitor workspace changed page heading count: %s", body)
	}
}

func TestMonitorAvailabilityChartUsesRequestNonceForInlineRuntime(t *testing.T) {
	monitorID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	data := MonitorList{
		Monitors: []sdk.Monitor{{Id: monitorID, Name: "Home DNS"}},
		Health:   map[string]sdk.MonitorHealth{monitorID.String(): {MonitorId: monitorID, State: sdk.Up}},
		Selected: monitorID.String(),
	}
	var rendered strings.Builder
	ctx := templ.WithNonce(t.Context(), "chart-test-nonce")
	if err := MonitorContent(data).Render(ctx, &rendered); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	if strings.Count(body, `nonce="chart-test-nonce"`) < 3 {
		t.Fatalf("chart inline runtime scripts missing request nonce: %s", body)
	}
	if strings.Contains(body, "let goecharts_") || !strings.Contains(body, "var goecharts_") {
		t.Fatal("chart renderer script is not safe for repeated HTMX swaps")
	}
	if strings.Contains(body, "<script>") {
		t.Fatal("chart rendered an inline script without CSP nonce")
	}
}

func TestAvailabilitySeedSnapshotPlacesCurrentStateAtRightEdge(t *testing.T) {
	now := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	snapshot := availabilitySeedSnapshot(sdk.Down, now)
	if got, want := len(snapshot.Categories), 37; got != want {
		t.Fatalf("seed categories = %d, want %d", got, want)
	}
	if got, want := snapshot.Categories[0], "09:00:00"; got != want {
		t.Fatalf("seed first category = %q, want %q", got, want)
	}
	if got, want := snapshot.Categories[len(snapshot.Categories)-1], "12:00:00"; got != want {
		t.Fatalf("seed latest category = %q, want %q", got, want)
	}
	for _, series := range snapshot.Series {
		if len(series.Values) != len(snapshot.Categories) {
			t.Fatalf("%s values = %d, want %d", series.Name, len(series.Values), len(snapshot.Categories))
		}
		for index, value := range series.Values[:len(series.Values)-1] {
			if value != 0 {
				t.Errorf("%s seed value[%d] = %v, want 0", series.Name, index, value)
			}
		}
	}
	if got := snapshot.Series[2].Values[len(snapshot.Categories)-1]; got != 1 {
		t.Fatalf("down latest value = %v, want 1", got)
	}
}

func TestMonitorContentNamesUnavailableSelectionWithoutReplacingTheList(t *testing.T) {
	var rendered strings.Builder
	if err := MonitorContent(MonitorList{Selected: "missing-monitor"}).Render(t.Context(), &rendered); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	for _, want := range []string{"No monitors yet", "Selected monitor unavailable", "Close detail"} {
		if !strings.Contains(body, want) {
			t.Errorf("unavailable selection missing %q", want)
		}
	}
}
