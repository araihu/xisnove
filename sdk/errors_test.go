package sdk_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/araihu/xisnove/sdk"
)

func TestErrorFromResponseDecodesRFC9457WithoutLeakingDetail(t *testing.T) {
	response := &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header)}
	body := []byte(`{
		"type":"https://xisnove.dev/problems/rate-limit",
		"title":"Rate limited",
		"status":429,
		"code":"rate_limited",
		"correlationId":"request-42",
		"detail":"provider secret must not be formatted",
		"instance":"https://provider.invalid/credentials/secret-instance",
		"fieldErrors":[{"field":"limit","message":"try later"}]
	}`)
	err := sdk.ErrorFromResponse(response, body)
	var apiError *sdk.APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("error = %#v", err)
	}
	if apiError.StatusCode != 429 || apiError.Problem.Code != "rate_limited" ||
		apiError.Problem.CorrelationId != "request-42" || apiError.Problem.FieldErrors == nil ||
		len(*apiError.Problem.FieldErrors) != 1 {
		t.Fatalf("API error = %#v", apiError)
	}
	if apiError.Problem.Detail != nil || apiError.Problem.Instance != nil {
		t.Fatalf("untrusted problem diagnostics retained: %#v", apiError.Problem)
	}
	if strings.Contains(err.Error(), "provider secret") || !strings.Contains(err.Error(), "request-42") {
		t.Fatalf("redacted error text = %q", err)
	}
}

func TestErrorFromResponseUsesStableFallbackAndSuccessSemantics(t *testing.T) {
	response := &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header)}
	err := sdk.ErrorFromResponse(response, []byte("upstream included opaque diagnostics"))
	var apiError *sdk.APIError
	if !errors.As(err, &apiError) || apiError.Problem.Code != "http_error" ||
		strings.Contains(err.Error(), "opaque diagnostics") {
		t.Fatalf("fallback error = %#v / %q", err, err)
	}
	response.StatusCode = http.StatusNoContent
	if err := sdk.ErrorFromResponse(response, nil); err != nil {
		t.Fatalf("successful response error = %v", err)
	}
	if !errors.Is(sdk.ErrorFromResponse(nil, nil), sdk.ErrMissingHTTPResponse) {
		t.Fatal("nil response did not return ErrMissingHTTPResponse")
	}
}
