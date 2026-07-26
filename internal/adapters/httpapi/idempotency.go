package httpapi

import (
	"errors"
	"net/http"

	"github.com/araihu/xisnove/application"
)

func requiredIdempotencyKey(value *IdempotencyKey) (string, error) {
	if value == nil || *value == "" {
		return "", &application.ValidationError{Fields: map[string]string{
			"idempotencyKey": "is required",
		}}
	}
	return string(*value), nil
}

func idempotencyProblemFromError(err error) (Problem, int, bool) {
	if errors.Is(err, application.ErrIdempotencyKeyReused) {
		return Problem{
			Type:          "https://xisnove.dev/problems/idempotency-key-reused",
			Title:         "Idempotency key reused",
			Status:        http.StatusConflict,
			Code:          "idempotency_key_reused",
			CorrelationId: "unknown",
		}, http.StatusConflict, true
	}
	if errors.Is(err, application.ErrCredentialAlreadyIssued) {
		return Problem{
			Type:          "https://xisnove.dev/problems/credential-already-issued",
			Title:         "Credential already issued",
			Status:        http.StatusConflict,
			Code:          "credential_already_issued",
			CorrelationId: "unknown",
		}, http.StatusConflict, true
	}
	return problemFromError(err)
}
