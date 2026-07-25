package problem_test

import (
	"errors"
	"testing"

	"github.com/araihu/xisnove/cli/internal/problem"
)

func TestFromHTTPReturnsTypedRFC9457ProblemWithExtensions(t *testing.T) {
	body := []byte(`{
  "type": "https://xisnove.dev/problems/validation",
  "title": "Validation failed",
  "status": 422,
  "detail": "request fields are invalid",
  "instance": "/v1/monitors",
  "code": "validation_failed",
  "correlationId": "corr-123",
  "fieldErrors": [{"field":"name","message":"is required"}]
}`)

	err := problem.FromHTTP(422, body)
	var typed *problem.Error
	if !errors.As(err, &typed) {
		t.Fatalf("FromHTTP() = %T, want *problem.Error", err)
	}
	if typed.Type != "https://xisnove.dev/problems/validation" || typed.Status != 422 || typed.Code != "validation_failed" {
		t.Fatalf("problem = %#v", typed)
	}
	if got := typed.Error(); got != "Validation failed: request fields are invalid (correlation corr-123)" {
		t.Fatalf("Error() = %q", got)
	}
	if got := typed.ExitCode(); got != 2 {
		t.Fatalf("ExitCode() = %d, want 2", got)
	}
}

func TestFromHTTPDoesNotEchoMalformedResponseBody(t *testing.T) {
	err := problem.FromHTTP(502, []byte(`upstream leaked token secret-value`))
	if err.Type != "about:blank" || err.Title != "Bad Gateway" || err.Status != 502 {
		t.Fatalf("FromHTTP() = %#v", err)
	}
	if got := err.Error(); got != "Bad Gateway" {
		t.Fatalf("Error() = %q, want body-redacted fallback", got)
	}
	if got := err.ExitCode(); got != 1 {
		t.Fatalf("ExitCode() = %d, want 1", got)
	}
}

func TestExitCodesAreStableByProblemClass(t *testing.T) {
	tests := []struct {
		status int
		want   int
	}{
		{status: 400, want: 2},
		{status: 401, want: 4},
		{status: 403, want: 4},
		{status: 404, want: 5},
		{status: 409, want: 6},
		{status: 429, want: 7},
		{status: 500, want: 1},
	}
	for _, tt := range tests {
		if got := problem.FromHTTP(tt.status, nil).ExitCode(); got != tt.want {
			t.Fatalf("status %d exit code = %d, want %d", tt.status, got, tt.want)
		}
	}
}
