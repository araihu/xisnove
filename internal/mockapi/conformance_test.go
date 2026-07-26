package mockapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAPIResponseValidationRejectsContractViolations(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
	}{
		{
			name:        "unsupported content type",
			status:      http.StatusCreated,
			contentType: "text/plain",
			body:        `{"token":"xisnove_mock_session_fixture_000000000001","expiresAt":"2026-07-26T00:00:00Z"}`,
		},
		{
			name:        "missing required response field",
			status:      http.StatusCreated,
			contentType: "application/json",
			body:        `{}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := conformanceHandler(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			response := httptest.NewRecorder()
			request := validSessionRequest()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500, body = %s", response.Code, response.Body.String())
			}
			var problem Problem
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Code != "mock_response_failed" {
				t.Fatalf("problem = %#v", problem)
			}
		})
	}
}

func TestOpenAPIResponseValidationRejectsUnsupportedStatus(t *testing.T) {
	spec, err := GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	spec.Paths.Value("/v1/sessions").Post.Responses.Delete("default")
	handler, err := newOpenAPIConformanceHandler(spec, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"token":"xisnove_mock_session_fixture_000000000001","expiresAt":"2026-07-26T00:00:00Z"}`))
	}))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, validSessionRequest())

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body = %s", response.Code, response.Body.String())
	}
}

func TestOpenAPIResponseValidationPassesAValidResponse(t *testing.T) {
	want := `{"token":"xisnove_mock_session_fixture_000000000001","expiresAt":"2026-07-26T00:00:00Z"}`
	handler := conformanceHandler(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(want))
	}))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, validSessionRequest())

	if response.Code != http.StatusCreated || response.Body.String() != want {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func conformanceHandler(t *testing.T, next http.Handler) http.Handler {
	t.Helper()
	spec, err := GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newOpenAPIConformanceHandler(spec, next)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func validSessionRequest() *http.Request {
	request := httptest.NewRequest(
		http.MethodPost,
		"http://mock.test/v1/sessions",
		strings.NewReader(`{"email":"admin@xisnove.test","password":"mock-password"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	return request
}
