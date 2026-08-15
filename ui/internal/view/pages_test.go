package view

import (
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	chartsassets "github.com/araihu/goshtoso-charts/assets"
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

func TestDocumentEmitsRouteSpecificSocialMetadataOnce(t *testing.T) {
	var rendered strings.Builder
	if err := LoginPage("csrf-token", "").Render(t.Context(), &rendered); err != nil {
		t.Fatalf("render login document: %v", err)
	}
	body := rendered.String()
	for _, want := range []string{
		`<title>Sign in · X-9</title>`,
		`<meta name="description" content="Sign in to Xisnove to inspect monitor health, lifecycle, and immutable state history.">`,
		`<link rel="canonical" href="https://x9.araihu.com/login">`,
		`<meta property="og:url" content="https://x9.araihu.com/login">`,
		`<meta property="og:type" content="website">`,
		`<meta property="og:title" content="Sign in · X-9">`,
		`<meta property="og:description" content="Sign in to Xisnove to inspect monitor health, lifecycle, and immutable state history.">`,
		`<meta property="og:site_name" content="Xisnove">`,
		`<meta property="og:locale" content="en_US">`,
		`<meta property="og:image" content="https://x9.araihu.com/ui/seasonal/v0.1.1/x9-social-preview.png">`,
		`<meta property="og:image:type" content="image/png">`,
		`<meta property="og:image:width" content="1280">`,
		`<meta property="og:image:height" content="640">`,
		`<meta property="og:image:alt" content="X-9 monitoring by Xisnove">`,
		`<meta name="twitter:card" content="summary_large_image">`,
		`<meta name="twitter:title" content="Sign in · X-9">`,
		`<meta name="twitter:description" content="Sign in to Xisnove to inspect monitor health, lifecycle, and immutable state history.">`,
		`<meta name="twitter:image" content="https://x9.araihu.com/ui/seasonal/v0.1.1/x9-social-preview.png">`,
		`<meta name="twitter:image:alt" content="X-9 monitoring by Xisnove">`,
	} {
		if strings.Count(body, want) != 1 {
			t.Errorf("initial document metadata count for %q = %d", want, strings.Count(body, want))
		}
	}
	if strings.Contains(body, "localhost") || strings.Contains(body, "example.test") {
		t.Fatal("initial document metadata contains a non-production host")
	}
}

func TestConsolePageEmitsSocialMetadataWithoutHeadTagDuplication(t *testing.T) {
	var rendered strings.Builder
	if err := ConsolePage("Monitors", "csrf-token", MonitorContent(MonitorList{})).Render(t.Context(), &rendered); err != nil {
		t.Fatalf("render console document: %v", err)
	}
	body := rendered.String()
	for _, want := range []string{
		`<title>Monitors · X-9</title>`,
		`<meta name="description" content="Inspect Xisnove monitor health, lifecycle, provenance, and bounded state history.">`,
		`<link rel="canonical" href="https://x9.araihu.com/monitors">`,
		`<meta property="og:url" content="https://x9.araihu.com/monitors">`,
		`<meta property="og:type" content="website">`,
		`<meta property="og:title" content="Monitors · X-9">`,
		`<meta property="og:description" content="Inspect Xisnove monitor health, lifecycle, provenance, and bounded state history.">`,
		`<meta property="og:site_name" content="Xisnove">`,
		`<meta property="og:image" content="https://x9.araihu.com/ui/seasonal/v0.1.1/x9-social-preview.png">`,
		`<meta property="og:image:type" content="image/png">`,
		`<meta property="og:image:width" content="1280">`,
		`<meta property="og:image:height" content="640">`,
		`<meta property="og:image:alt" content="X-9 monitoring by Xisnove">`,
		`<meta name="twitter:card" content="summary_large_image">`,
		`<meta name="twitter:title" content="Monitors · X-9">`,
		`<meta name="twitter:description" content="Inspect Xisnove monitor health, lifecycle, provenance, and bounded state history.">`,
		`<meta name="twitter:image" content="https://x9.araihu.com/ui/seasonal/v0.1.1/x9-social-preview.png">`,
		`<meta name="twitter:image:alt" content="X-9 monitoring by Xisnove">`,
	} {
		if strings.Count(body, want) != 1 {
			t.Errorf("console document metadata count for %q = %d", want, strings.Count(body, want))
		}
	}
}

func TestConsolePageIncludesChartDependenciesWithRequestNonce(t *testing.T) {
	var rendered strings.Builder
	ctx := templ.WithNonce(t.Context(), "console-chart-nonce")
	if err := ConsolePage("Monitors", "csrf-token", MonitorContent(MonitorList{})).Render(ctx, &rendered); err != nil {
		t.Fatalf("render console document: %v", err)
	}
	body := rendered.String()
	chartRuntime := `<script src="` + chartsassets.RuntimeURL + `" nonce="console-chart-nonce"></script>`
	if strings.Count(body, chartRuntime) != 1 {
		t.Fatalf("chart runtime script count = %d, want 1: %s", strings.Count(body, chartRuntime), body)
	}
	if strings.Count(body, chartsassets.RuntimeURL) != 1 {
		t.Fatalf("chart runtime URL was duplicated")
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

func TestAvailabilityChartUsesNeutralUnknownSeries(t *testing.T) {
	var rendered strings.Builder
	if err := AppStyles().Render(t.Context(), &rendered); err != nil {
		t.Fatalf("render app styles: %v", err)
	}
	body := rendered.String()
	for _, want := range []string{
		`.xis-availability-chart { margin: .25rem 0 0; width: 100%; --color-chart-series-4: var(--color-on-surface-muted) !important; }`,
		`.dark .xis-availability-chart { --color-chart-series-4: var(--color-on-surface-dark-muted) !important; }`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("availability chart style missing %q", want)
		}
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

func TestMonitorDrawerCloseUsesHTMXPushOption(t *testing.T) {
	monitorID := uuid.MustParse("10000000-0000-4000-8000-000000000016")
	data := MonitorList{
		Monitors: []sdk.Monitor{{Id: monitorID, Name: "Live monitor", Enabled: true}},
		Health:   map[string]sdk.MonitorHealth{monitorID.String(): {MonitorId: monitorID, State: sdk.Up}},
		Selected: monitorID.String(),
	}

	var rendered strings.Builder
	if err := MonitorContent(data).Render(t.Context(), &rendered); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	if !strings.Contains(body, `window.htmx.ajax('GET', closeURL, {target: '#main-content', swap: 'outerHTML', push: true})`) {
		t.Fatalf("drawer close does not request an HTMX URL push: %s", body)
	}
	if strings.Contains(body, "pushURL") {
		t.Fatal("drawer close uses the unsupported htmx.ajax pushURL option")
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

func TestMonitorContentRendersBoundedStateTickHistoryAndProvenance(t *testing.T) {
	monitorID := uuid.MustParse("10000000-0000-4000-8000-000000000011")
	locationID := uuid.MustParse("20000000-0000-4000-8000-000000000011")
	actionID := uuid.MustParse("50000000-0000-4000-8000-000000000011")
	actorID := uuid.MustParse("60000000-0000-4000-8000-000000000011")
	userActionID := uuid.MustParse("30000000-0000-4000-8000-000000000011")
	observationID := uuid.MustParse("70000000-0000-4000-8000-000000000011")
	causalTickID := uuid.MustParse("80000000-0000-4000-8000-000000000011")
	causalDependencyID := uuid.MustParse("40000000-0000-4000-8000-000000000011")
	base := time.Date(2026, time.August, 15, 9, 0, 0, 0, time.UTC)
	history := sdk.MonitorStateHistory{
		MonitorId: monitorID,
		StartsAt:  base,
		EndsAt:    base.Add(3 * time.Hour),
		Ticks: []sdk.MonitorStateTick{
			{MonitorId: monitorID, LocationId: &locationID, Lifecycle: sdk.Active, Health: sdk.Up, ReasonCode: sdk.StateTickReasonCodeProbeSuccess, Actor: sdk.StateTickActor{Kind: sdk.StateTickActorKindSystem}, OccurredAt: base.Add(15 * time.Minute)},
			{MonitorId: monitorID, LocationId: &locationID, Lifecycle: sdk.Active, Health: sdk.Degraded, ReasonCode: sdk.StateTickReasonCodeProbeFailure, Actor: sdk.StateTickActor{Kind: sdk.StateTickActorKindAgent}, OccurredAt: base.Add(75 * time.Minute)},
			{MonitorId: monitorID, Lifecycle: sdk.Paused, Health: sdk.Unknown, ReasonCode: sdk.StateTickReasonCodeDependencyPaused, ActionId: actionID, UserActionId: &userActionID, ObservationId: &observationID, CausalTickId: &causalTickID, CausalDependencyId: &causalDependencyID, Actor: sdk.StateTickActor{Id: &actorID, Kind: sdk.StateTickActorKindUser}, OccurredAt: base.Add(150 * time.Minute)},
		},
	}
	data := MonitorList{
		Monitors:     []sdk.Monitor{{Id: monitorID, Name: "Home DNS", Description: "Resolver reachability", Kind: sdk.MonitorKindDns, Enabled: true, LocationId: locationID}},
		Health:       map[string]sdk.MonitorHealth{monitorID.String(): {MonitorId: monitorID, State: sdk.Unknown}},
		StateHistory: map[string]sdk.MonitorStateHistory{monitorID.String(): history},
		Selected:     monitorID.String(),
	}

	var rendered strings.Builder
	if err := MonitorContent(data).Render(t.Context(), &rendered); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	for _, want := range []string{
		"State history", "Last 3 hours.", "Lifecycle", "Health", "Reason", "Provenance",
		"Active", "Paused", "UP", "DEGRADED", "UNKNOWN", "probe_success", "probe_failure", "dependency_paused",
		"system", "agent", "user", "User action", "Causal dependency",
		`data-state-history-window="3h"`, `data-state-health="unknown"`, `data-state-health="degraded"`,
		"xis-state-unknown", "xis-state-warning",
		`data-state-tick="true"`, `data-state-action-id="` + actionID.String() + `"`, `data-state-actor-id="` + actorID.String() + `"`, `data-state-actor-kind="user"`,
		`data-state-user-action-id="` + userActionID.String() + `"`, `data-state-observation-id="` + observationID.String() + `"`, `data-state-causal-tick-id="` + causalTickID.String() + `"`, `data-state-causal-dependency-id="` + causalDependencyID.String() + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("state history drawer missing %q", want)
		}
	}
	if strings.Index(body, "probe_success") >= strings.Index(body, "probe_failure") || strings.Index(body, "probe_failure") >= strings.Index(body, "dependency_paused") {
		t.Fatalf("state ticks lost chronological order: %s", body)
	}
	if strings.Contains(body, `><code>`+monitorID.String()+`</code><`) || strings.Contains(body, `><code>`+locationID.String()+`</code><`) {
		t.Fatalf("drawer rendered raw technical identifiers: %s", body)
	}
	tableStart, tableEnd := strings.Index(body, "<table"), strings.Index(body, "</table>")
	if tableStart < 0 || tableEnd < tableStart {
		t.Fatalf("monitor list table missing: %s", body)
	}
	listMarkup := body[tableStart:tableEnd]
	if strings.Contains(listMarkup, ">"+monitorID.String()+"<") || strings.Contains(listMarkup, ">"+locationID.String()+"<") {
		t.Fatalf("monitor list exposed raw identifiers: %s", listMarkup)
	}
}

func TestMonitorContentTrimsStateTicksToLatestThreeHours(t *testing.T) {
	monitorID := uuid.MustParse("10000000-0000-4000-8000-000000000012")
	base := time.Date(2026, time.August, 15, 9, 0, 0, 0, time.UTC)
	history := sdk.MonitorStateHistory{
		MonitorId: monitorID,
		StartsAt:  base,
		EndsAt:    base.Add(4 * time.Hour),
		Ticks: []sdk.MonitorStateTick{
			{MonitorId: monitorID, Lifecycle: sdk.Active, Health: sdk.Up, ReasonCode: sdk.StateTickReasonCodeInitial, Actor: sdk.StateTickActor{Kind: sdk.StateTickActorKindSystem}, OccurredAt: base.Add(30 * time.Minute)},
			{MonitorId: monitorID, Lifecycle: sdk.Active, Health: sdk.Degraded, ReasonCode: sdk.StateTickReasonCodeProbeFailure, Actor: sdk.StateTickActor{Kind: sdk.StateTickActorKindSystem}, OccurredAt: base.Add(90 * time.Minute)},
			{MonitorId: monitorID, Lifecycle: sdk.Active, Health: sdk.Up, ReasonCode: sdk.StateTickReasonCodeProbeSuccess, Actor: sdk.StateTickActor{Kind: sdk.StateTickActorKindSystem}, OccurredAt: base.Add(3*time.Hour + 30*time.Minute)},
		},
	}
	data := MonitorList{
		Monitors:     []sdk.Monitor{{Id: monitorID, Name: "Windowed monitor", Enabled: true}},
		Health:       map[string]sdk.MonitorHealth{monitorID.String(): {MonitorId: monitorID, State: sdk.Up}},
		StateHistory: map[string]sdk.MonitorStateHistory{monitorID.String(): history},
		Selected:     monitorID.String(),
	}

	var rendered strings.Builder
	if err := MonitorContent(data).Render(t.Context(), &rendered); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	if strings.Contains(body, "initial") {
		t.Fatal("state history rendered tick older than three-hour window")
	}
	for _, want := range []string{"probe_failure", "probe_success", "15 Aug 2026 12:30 UTC"} {
		if !strings.Contains(body, want) {
			t.Errorf("bounded state history missing %q", want)
		}
	}
}

func TestMonitorContentRendersStateHistoryErrorInsteadOfAnEmptyGap(t *testing.T) {
	monitorID := uuid.MustParse("10000000-0000-4000-8000-000000000013")
	data := MonitorList{
		Monitors:           []sdk.Monitor{{Id: monitorID, Name: "Unavailable history", Enabled: true}},
		Health:             map[string]sdk.MonitorHealth{monitorID.String(): {MonitorId: monitorID, State: sdk.Unknown}},
		StateHistoryErrors: map[string]string{monitorID.String(): "Control-plane history is temporarily unavailable."},
		Selected:           monitorID.String(),
	}

	var rendered strings.Builder
	if err := MonitorContent(data).Render(t.Context(), &rendered); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	for _, want := range []string{"State history unavailable", "Control-plane history is temporarily unavailable.", `data-state-history-status="error"`} {
		if !strings.Contains(body, want) {
			t.Errorf("state history error missing %q", want)
		}
	}
	for _, want := range []string{`data-state-history-error`, `data-state-ticks-list`, `data-state-history-gap hidden`} {
		if !strings.Contains(body, want) {
			t.Errorf("state history error cannot be replaced by SSE: missing %q", want)
		}
	}
}

func TestMonitorContentRendersGapForStateHistoryThatCanBecomeEmpty(t *testing.T) {
	monitorID := uuid.MustParse("10000000-0000-4000-8000-000000000016")
	history := sdk.MonitorStateHistory{
		MonitorId: monitorID,
		StartsAt:  time.Date(2026, time.August, 15, 9, 0, 0, 0, time.UTC),
		EndsAt:    time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC),
		Ticks: []sdk.MonitorStateTick{{
			MonitorId: monitorID, Lifecycle: sdk.Active, Health: sdk.Up,
			ReasonCode: sdk.StateTickReasonCodeProbeSuccess,
			Actor:      sdk.StateTickActor{Kind: sdk.StateTickActorKindSystem},
			OccurredAt: time.Date(2026, time.August, 15, 11, 0, 0, 0, time.UTC),
		}},
	}
	data := MonitorList{
		Monitors:     []sdk.Monitor{{Id: monitorID, Name: "Recovering history", Enabled: true}},
		Health:       map[string]sdk.MonitorHealth{monitorID.String(): {MonitorId: monitorID, State: sdk.Up}},
		StateHistory: map[string]sdk.MonitorStateHistory{monitorID.String(): history},
		Selected:     monitorID.String(),
	}

	var rendered strings.Builder
	if err := MonitorContent(data).Render(t.Context(), &rendered); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	if !strings.Contains(body, `data-state-history-gap hidden`) {
		t.Fatalf("state history with records has no hidden empty gap: %s", body)
	}
}

func TestMonitorDrawerTitleNamesMonitorLifecycleAndHealthAccessibly(t *testing.T) {
	monitorID := uuid.MustParse("10000000-0000-4000-8000-000000000014")
	base := time.Date(2026, time.August, 15, 11, 0, 0, 0, time.UTC)
	data := MonitorList{
		Monitors: []sdk.Monitor{{Id: monitorID, Name: "Paused monitor", Enabled: true}},
		Health:   map[string]sdk.MonitorHealth{monitorID.String(): {MonitorId: monitorID, State: sdk.Unknown}},
		StateHistory: map[string]sdk.MonitorStateHistory{monitorID.String(): {
			MonitorId: monitorID,
			StartsAt:  base.Add(-3 * time.Hour),
			EndsAt:    base,
			Ticks: []sdk.MonitorStateTick{{
				MonitorId: monitorID, Lifecycle: sdk.Paused, Health: sdk.Unknown,
				ReasonCode: sdk.StateTickReasonCodePausedByUser,
				Actor:      sdk.StateTickActor{Kind: sdk.StateTickActorKindUser}, OccurredAt: base.Add(-time.Minute),
			}},
		}},
		Selected: monitorID.String(),
	}

	var rendered strings.Builder
	if err := MonitorContent(data).Render(t.Context(), &rendered); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	titleStart := strings.Index(body, `id="monitor-detail-drawerTitle"`)
	if titleStart < 0 {
		t.Fatalf("drawer title missing: %s", body)
	}
	titleEnd := strings.Index(body[titleStart:], "</")
	if titleEnd < 0 {
		t.Fatalf("drawer title is not closed: %s", body)
	}
	title := body[titleStart : titleStart+titleEnd]
	for _, want := range []string{"Paused monitor", "Paused", "UNKNOWN"} {
		if !strings.Contains(title, want) {
			t.Errorf("accessible drawer title missing %q: %s", want, title)
		}
	}
	if strings.Contains(title, "::after") {
		t.Fatal("drawer title relies on a CSS pseudo-element for status")
	}
}

func TestMonitorDetailInstallsStateTickSSEConsumer(t *testing.T) {
	monitorID := uuid.MustParse("10000000-0000-4000-8000-000000000015")
	data := MonitorList{
		Monitors: []sdk.Monitor{{Id: monitorID, Name: "Live monitor", Enabled: true}},
		Health:   map[string]sdk.MonitorHealth{monitorID.String(): {MonitorId: monitorID, State: sdk.Up}},
		Selected: monitorID.String(),
	}

	var rendered strings.Builder
	ctx := templ.WithNonce(t.Context(), "state-tick-consumer-nonce")
	if err := MonitorContent(data).Render(ctx, &rendered); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	for _, want := range []string{
		`data-state-ticks-consumer="true"`, `addEventListener('state-ticks'`, `new EventSource(`,
		`data-state-ticks-list`, `xisnove:state-ticks`, `State history updated`,
		`const monitorID = owner?.dataset.monitorId`, `encodeURIComponent(monitorID) + '/availability/events'`,
		`item.dataset.stateActionId`, `item.dataset.stateActorId`, `item.dataset.stateActorKind`, `item.dataset.stateUserActionId`, `item.dataset.stateObservationId`, `item.dataset.stateCausalTickId`, `item.dataset.stateCausalDependencyId`,
		`data-state-history-error`, `if (error) error.hidden = true`, `source.close(); window.location.assign('/login')`,
		`const hasID`, `if (hasID(tick.actionId))`, `if (hasID(tick.actor?.id))`, `if (hasID(tick.userActionId))`, `if (hasID(tick.observationId))`, `if (hasID(tick.causalTickId))`, `if (hasID(tick.causalDependencyId))`,
		`const provenanceParts`, `Action recorded`, `Actor identity recorded`, `User action linked`, `Observation linked`, `Causal state tick linked`, `Causal dependency linked`, `provenanceParts.join(' · ')`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("state tick SSE consumer missing %q", want)
		}
	}
	if !strings.Contains(body, `nonce="state-tick-consumer-nonce"`) {
		t.Fatal("state tick SSE consumer did not receive request nonce")
	}
}
