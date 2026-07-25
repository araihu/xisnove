package shoutrrr_test

import (
	"slices"
	"testing"

	shoutrrradapter "github.com/araihu/xisnove/internal/adapters/shoutrrr"
)

func TestReviewedSchemesRequireInjectedHTTPClientSupport(t *testing.T) {
	for _, scheme := range []string{"discord", "generic", "gotify", "slack", "telegram", "twilio"} {
		if !shoutrrradapter.SchemeReviewed(scheme) {
			t.Errorf("expected reviewed HTTP scheme %q", scheme)
		}
	}
	for _, scheme := range []string{"smtp", "mqtt", "mqtts", "logger", "xmpp", ""} {
		if shoutrrradapter.SchemeReviewed(scheme) {
			t.Errorf("non-reviewed network scheme %q was accepted", scheme)
		}
	}
	values := shoutrrradapter.ReviewedSchemes()
	values[0] = "mutated"
	if slices.Contains(shoutrrradapter.ReviewedSchemes(), "mutated") {
		t.Fatal("ReviewedSchemes returned mutable package state")
	}
}
