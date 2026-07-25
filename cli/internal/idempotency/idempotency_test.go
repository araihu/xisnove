package idempotency_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/araihu/xisnove/cli/internal/idempotency"
)

func TestResolveGeneratesOnceReportsOnStderrAndReusesHeaderValue(t *testing.T) {
	var diagnostics bytes.Buffer
	calls := 0
	policy := idempotency.Policy{
		Diagnostics: &diagnostics,
		Generate: func() (string, error) {
			calls++
			return "018f-example-key", nil
		},
	}

	key, err := policy.Resolve("")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if key != "018f-example-key" || calls != 1 {
		t.Fatalf("Resolve() = %q, calls = %d", key, calls)
	}
	if got := diagnostics.String(); got != "generated idempotency key: 018f-example-key\n" {
		t.Fatalf("diagnostics = %q", got)
	}

	editor := idempotency.HeaderEditor(key)
	for i := 0; i < 2; i++ {
		req, err := http.NewRequest(http.MethodPost, "https://example.test/v1/resource", nil)
		if err != nil {
			t.Fatalf("NewRequest() error = %v", err)
		}
		if err := editor(context.Background(), req); err != nil {
			t.Fatalf("editor() error = %v", err)
		}
		if got := req.Header.Get("Idempotency-Key"); got != key {
			t.Fatalf("Idempotency-Key = %q, want %q", got, key)
		}
	}
}

func TestResolveUsesExplicitKeyWithoutDiagnostics(t *testing.T) {
	var diagnostics bytes.Buffer
	key, err := (idempotency.Policy{Diagnostics: &diagnostics}).Resolve("deploy-2026-07-25:monitor.1")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if key != "deploy-2026-07-25:monitor.1" {
		t.Fatalf("Resolve() = %q", key)
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("diagnostics = %q, want empty", diagnostics.String())
	}
}

func TestResolveRejectsUnsafeExplicitKey(t *testing.T) {
	tests := []string{"contains space", "line\nbreak", "", string(bytes.Repeat([]byte{'a'}, 129))}
	for _, value := range tests {
		if value == "" {
			continue // empty explicitly means generate
		}
		if _, err := (idempotency.Policy{}).Resolve(value); err == nil {
			t.Fatalf("Resolve(%q) error = nil, want validation error", value)
		}
	}
}

func TestGeneratedKeyIsRFC4122UUID(t *testing.T) {
	key, err := (idempotency.Policy{}).Resolve("")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(key) != 36 || key[14] != '4' || (key[19] != '8' && key[19] != '9' && key[19] != 'a' && key[19] != 'b') {
		t.Fatalf("generated key = %q, want RFC 4122 UUID v4", key)
	}
}
