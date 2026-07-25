package httpapi

import (
	"errors"
	"net/http"

	"github.com/araihu/xisnove/internal/application"
)

func ToProblem(err error, correlationID string) Problem {
	if problem, _, ok := problemFromError(err); ok {
		problem.CorrelationId = correlationID
		return problem
	}
	detail := "An unexpected error occurred."
	return Problem{
		Type:          "https://xisnove.dev/problems/internal",
		Title:         "Internal server error",
		Status:        http.StatusInternalServerError,
		Code:          "internal_error",
		CorrelationId: correlationID,
		Detail:        &detail,
	}
}

func statusForProblem(problem Problem) int {
	if problem.Status >= 400 && problem.Status <= 599 {
		return int(problem.Status)
	}
	return http.StatusInternalServerError
}

func isAuthenticationError(err error) bool {
	return errors.Is(err, application.ErrInvalidCredentials) ||
		errors.Is(err, application.ErrInvalidEnrollmentToken)
}
