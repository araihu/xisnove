package application_test

import (
	"strings"
	"testing"

	"github.com/araihu/xisnove/application"
)

func TestTransportResultScrubsAndBoundsDiagnostics(t *testing.T) {
	result := application.NewTransportResult(
		application.TransportTransientFailure,
		"provider_unavailable",
		"Authorization: Bearer abc https://user:pass@example.com/hook?token=secret "+strings.Repeat("x", 2000),
		"receipt-1",
		"abc", "secret",
	)
	if result.Outcome != application.TransportTransientFailure || result.ErrorClass != "provider_unavailable" || result.ProviderReceipt != "receipt-1" {
		t.Fatalf("result = %#v", result)
	}
	if strings.Contains(result.Diagnostic, "abc") || strings.Contains(result.Diagnostic, "secret") ||
		strings.Contains(result.Diagnostic, "user:pass") || len(result.Diagnostic) > application.MaxTransportDiagnosticBytes {
		t.Fatalf("diagnostic was not scrubbed and bounded: %q", result.Diagnostic)
	}
}

func TestTransportResultRejectsUnstableValues(t *testing.T) {
	result := application.NewTransportResult("invented", "UPPER CASE", "failure", "")
	if result.Outcome != application.TransportPermanentFailure || result.ErrorClass != "transport_invalid_result" {
		t.Fatalf("result = %#v", result)
	}
}
