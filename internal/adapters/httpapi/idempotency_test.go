package httpapi

import (
	"errors"
	"testing"

	"github.com/araihu/xisnove/application"
)

func TestRequiredIdempotencyKeyRejectsMissingAndEmpty(t *testing.T) {
	for name, value := range map[string]*IdempotencyKey{
		"missing": nil,
		"empty":   pointerToIdempotencyKey(""),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := requiredIdempotencyKey(value)
			var validation *application.ValidationError
			if !errors.As(err, &validation) || validation.Fields["idempotencyKey"] != "is required" {
				t.Fatalf("error = %v, want idempotencyKey ValidationError", err)
			}
		})
	}
	want := "opaque-client-key"
	got, err := requiredIdempotencyKey(pointerToIdempotencyKey(want))
	if err != nil || got != want {
		t.Fatalf("requiredIdempotencyKey() = %q, %v", got, err)
	}
}

func TestIdempotencyConflictProblemsAreStable(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		code    string
		typeURI string
	}{
		{name: "key reused", err: application.ErrIdempotencyKeyReused, code: "idempotency_key_reused", typeURI: "https://xisnove.dev/problems/idempotency-key-reused"},
		{name: "credential issued", err: application.ErrCredentialAlreadyIssued, code: "credential_already_issued", typeURI: "https://xisnove.dev/problems/credential-already-issued"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			problem, status, ok := idempotencyProblemFromError(test.err)
			if !ok || status != 409 || problem.Status != 409 || problem.Code != test.code || problem.Type != test.typeURI {
				t.Fatalf("problem = %#v, status = %d, mapped = %t", problem, status, ok)
			}
		})
	}
	if _, _, ok := idempotencyProblemFromError(errors.New("other")); ok {
		t.Fatal("unrelated error was mapped as idempotency conflict")
	}
}

func pointerToIdempotencyKey(value string) *IdempotencyKey {
	key := IdempotencyKey(value)
	return &key
}
