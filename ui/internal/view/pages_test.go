package view

import (
	"strings"
	"testing"
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
	if err := StatusContent().Render(t.Context(), &rendered); err != nil {
		t.Fatalf("render status content: %v", err)
	}
	if !strings.Contains(rendered.String(), `aria-live="polite"`) {
		t.Fatalf("status content is not a polite live region: %s", rendered.String())
	}
}
