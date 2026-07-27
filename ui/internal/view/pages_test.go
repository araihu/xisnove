package view

import (
	"strings"
	"testing"
	"time"

	"github.com/araihu/xisnove/sdk"
	"github.com/google/uuid"
)

func TestBrandUsesX9DisplayName(t *testing.T) {
	var rendered strings.Builder
	if err := Brand().Render(t.Context(), &rendered); err != nil {
		t.Fatalf("render brand: %v", err)
	}
	if body := rendered.String(); !strings.Contains(body, ">X-9<") || strings.Contains(body, "Xisnove") {
		t.Fatalf("unexpected display brand: %s", body)
	}
}

func TestDocumentLoadsAraiHuThemeAfterGoshtosoAndUsesItByDefault(t *testing.T) {
	var rendered strings.Builder
	if err := Document("Theme contract", Brand()).Render(t.Context(), &rendered); err != nil {
		t.Fatalf("render document: %v", err)
	}
	body := rendered.String()
	goshtoso := strings.Index(body, `/assets/styles.css`)
	araihu := strings.Index(body, `/ui/araihu-f841fe90.css`)
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

	rendered.Reset()
	if err := ThemeControls().Render(t.Context(), &rendered); err != nil {
		t.Fatalf("render theme controls: %v", err)
	}
	for _, want := range []string{
		`<option value="araihu">Arai Hû</option>`,
		`<option value="goshtoso">Goshtoso</option>`,
		`<option value="minimal">Minimal</option>`,
	} {
		if !strings.Contains(rendered.String(), want) {
			t.Errorf("theme controls contract missing %q", want)
		}
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
	for _, want := range []string{`data-preserve-focus`, `hx-disabled-elt="find button[type='submit']"`, `data-goshtoso-loading`, "Searching…"} {
		if !strings.Contains(body, want) {
			t.Errorf("real search loading contract missing %q", want)
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
		{name: "filtered", data: MonitorList{Query: "dns"}, want: []string{"No matching monitors", "Clear search", `value="dns"`}},
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
		`id="monitor-detail"`, `aria-labelledby="monitor-detail-heading"`,
		`data-monitor-id="` + monitorID.String() + `"`, `aria-selected="true"`,
		`id="monitor-detail-heading" tabindex="-1" data-autofocus`, "Home DNS", "DEGRADED",
		"Current health", "Configuration", "Observation history",
		"History is not exposed by the current public API",
		"60 seconds", "2500 ms", "3 failures", "2 successes",
		locationID.String(), "26 Jul 2026 12:30 UTC",
		`href="/monitors?cursor=opaque%2Fpage&amp;q=dns"`, "Close detail",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("selected monitor workspace missing %q", want)
		}
	}
	if strings.Count(body, "<h1") != 1 {
		t.Errorf("selected monitor workspace changed page heading count: %s", body)
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
