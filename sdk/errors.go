package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

var ErrMissingHTTPResponse = errors.New("HTTP response is missing")

// APIError is a structured, redacted RFC 9457 failure returned by Xisnove.
// It intentionally does not retain the raw response body, which may contain
// credentials or provider diagnostics from a non-conforming intermediary.
type APIError struct {
	StatusCode int
	Problem    Problem
}

func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Problem.Code != "" && e.Problem.CorrelationId != "" {
		return fmt.Sprintf("Xisnove API HTTP %d: %s (correlation %s)", e.StatusCode, e.Problem.Code, e.Problem.CorrelationId)
	}
	if e.Problem.Code != "" {
		return fmt.Sprintf("Xisnove API HTTP %d: %s", e.StatusCode, e.Problem.Code)
	}
	return fmt.Sprintf("Xisnove API HTTP %d", e.StatusCode)
}

// ErrorFromResponse returns nil for a successful response and a structured
// APIError for every non-2xx response. Malformed or non-problem bodies are
// represented by the stable fallback code http_error without exposing bytes.
func ErrorFromResponse(response *http.Response, body []byte) error {
	if response == nil {
		return ErrMissingHTTPResponse
	}
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		return nil
	}
	problem := Problem{
		Code: "http_error", CorrelationId: response.Header.Get("X-Request-ID"),
		Status: int32(response.StatusCode), Title: http.StatusText(response.StatusCode),
		Type: "about:blank",
	}
	var decoded Problem
	if json.Unmarshal(body, &decoded) == nil && decoded.Code != "" {
		problem = cloneProblem(decoded)
		problem.Status = int32(response.StatusCode)
	}
	return &APIError{StatusCode: response.StatusCode, Problem: problem}
}

func cloneProblem(problem Problem) Problem {
	if problem.FieldErrors != nil {
		fields := append([]FieldError(nil), (*problem.FieldErrors)...)
		problem.FieldErrors = &fields
	}
	return problem
}
