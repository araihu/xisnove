package httpapi_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/araihu/xisnove/internal/adapters/httpapi"
)

func TestProblemMappingDoesNotLeakInternalError(t *testing.T) {
	problem := httpapi.ToProblem(
		errors.New("sqlite: secret path /private/db failed"),
		"correlation-1",
	)
	if problem.Status != 500 || problem.Code != "internal_error" {
		t.Fatalf("problem = %#v", problem)
	}
	if problem.Detail != nil &&
		(strings.Contains(*problem.Detail, "sqlite") ||
			strings.Contains(*problem.Detail, "/private")) {
		t.Fatalf("leaked detail: %q", *problem.Detail)
	}
}
