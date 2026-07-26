package problem

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/araihu/xisnove/sdk"
)

type FieldError struct {
	Field   string `json:"field" yaml:"field"`
	Message string `json:"message" yaml:"message"`
}

// Error is an RFC 9457 problem detail with Xisnove's stable extensions.
type Error struct {
	Type          string       `json:"type" yaml:"type"`
	Title         string       `json:"title" yaml:"title"`
	Status        int          `json:"status" yaml:"status"`
	Detail        string       `json:"detail,omitempty" yaml:"detail,omitempty"`
	Instance      string       `json:"instance,omitempty" yaml:"instance,omitempty"`
	Code          string       `json:"code,omitempty" yaml:"code,omitempty"`
	CorrelationID string       `json:"correlationId,omitempty" yaml:"correlationId,omitempty"`
	FieldErrors   []FieldError `json:"fieldErrors,omitempty" yaml:"fieldErrors,omitempty"`
}

func (e *Error) Error() string {
	message := e.Title
	if e.Detail != "" {
		message += ": " + e.Detail
	}
	if e.CorrelationID != "" {
		message += fmt.Sprintf(" (correlation %s)", e.CorrelationID)
	}
	return message
}

func (e *Error) ExitCode() int {
	switch e.Status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return 2
	case http.StatusUnauthorized, http.StatusForbidden:
		return 4
	case http.StatusNotFound:
		return 5
	case http.StatusConflict:
		return 6
	case http.StatusTooManyRequests:
		return 7
	default:
		return 1
	}
}

func FromHTTP(status int, body []byte) *Error {
	response := &http.Response{StatusCode: status, Header: make(http.Header)}
	err := sdk.ErrorFromResponse(response, body)
	var apiError *sdk.APIError
	if !errors.As(err, &apiError) {
		return fallback(status)
	}
	remote := apiError.Problem
	problem := &Error{
		Type:          remote.Type,
		Title:         remote.Title,
		Status:        apiError.StatusCode,
		Code:          remote.Code,
		CorrelationID: remote.CorrelationId,
	}
	if remote.FieldErrors != nil {
		problem.FieldErrors = make([]FieldError, 0, len(*remote.FieldErrors))
		for _, field := range *remote.FieldErrors {
			problem.FieldErrors = append(problem.FieldErrors, FieldError{Field: field.Field, Message: field.Message})
		}
	}
	// Detail and instance are deliberately excluded. The SDK owns the remote
	// error allowlist so provider diagnostics or credentials cannot reach CLI
	// output through a nominal RFC 9457 response.
	if problem.Title == "" {
		problem.Title = http.StatusText(status)
	}
	return problem
}

func Usage(detail string) *Error {
	return &Error{
		Type:   "https://xisnove.dev/problems/cli-usage",
		Title:  "Invalid command usage",
		Status: http.StatusBadRequest,
		Detail: detail,
		Code:   "cli_usage",
	}
}

func ContractUnavailable(family string) *Error {
	return &Error{
		Type:   "https://xisnove.dev/problems/contract-unavailable",
		Title:  "Command unavailable",
		Status: http.StatusNotImplemented,
		Detail: fmt.Sprintf("%s commands require the frozen generated SDK contract", family),
		Code:   "contract_unavailable",
	}
}

func Local(status int, title, detail, code string) *Error {
	return &Error{
		Type:   "https://xisnove.dev/problems/" + code,
		Title:  title,
		Status: status,
		Detail: detail,
		Code:   code,
	}
}

func fallback(status int) *Error {
	title := http.StatusText(status)
	if title == "" {
		title = "HTTP request failed"
	}
	return &Error{Type: "about:blank", Title: title, Status: status}
}
