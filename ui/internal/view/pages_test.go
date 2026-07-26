package view

import (
	"strings"
	"testing"

	"github.com/araihu/xisnove/sdk"
)

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
	if !strings.Contains(rendered.String(), `aria-live="polite"`) {
		t.Fatalf("status content is not a polite live region: %s", rendered.String())
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
