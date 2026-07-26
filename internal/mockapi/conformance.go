package mockapi

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

func newOpenAPIConformanceHandler(spec *openapi3.T, next http.Handler) (http.Handler, error) {
	router, err := gorillamux.NewRouter(spec)
	if err != nil {
		return nil, fmt.Errorf("build OpenAPI mock router: %w", err)
	}
	options := &openapi3filter.Options{
		AuthenticationFunc: func(context.Context, *openapi3filter.AuthenticationInput) error {
			return nil
		},
		ExcludeWriteOnlyValidations: true,
		IncludeResponseStatus:       true,
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route, pathParams, err := router.FindRoute(r)
		if err != nil {
			writeProblem(w, r, http.StatusBadRequest, "validation_failed", "Request validation failed", nil)
			return
		}
		requestInput := &openapi3filter.RequestValidationInput{
			Request: r, PathParams: pathParams, Route: route, Options: options,
		}
		if route.Operation.RequestBody == nil && r.Body != nil && r.Body != http.NoBody && r.ContentLength != 0 {
			writeProblem(w, r, http.StatusBadRequest, "validation_failed", "Request validation failed", nil)
			return
		}
		if err := openapi3filter.ValidateRequest(r.Context(), requestInput); err != nil {
			writeProblem(w, r, http.StatusBadRequest, "validation_failed", "Request validation failed", nil)
			return
		}

		buffered := newBufferedResponseWriter()
		next.ServeHTTP(buffered, r)
		if err := validateOpenAPIResponse(r.Context(), requestInput, buffered); err != nil {
			writeProblem(w, r, http.StatusInternalServerError, "mock_response_failed", "Mock response failed", nil)
			return
		}
		buffered.writeTo(w)
	}), nil
}

func validateOpenAPIResponse(
	ctx context.Context,
	requestInput *openapi3filter.RequestValidationInput,
	response *bufferedResponseWriter,
) error {
	status := response.statusCode()
	responses := requestInput.Route.Operation.Responses
	responseRef := responses.Status(status)
	if responseRef == nil {
		responseRef = responses.Default()
	}
	if responseRef != nil && responseRef.Value != nil && len(responseRef.Value.Content) == 0 && len(bytes.TrimSpace(response.body.Bytes())) != 0 {
		return fmt.Errorf("status %d does not define a response body", status)
	}
	responseInput := (&openapi3filter.ResponseValidationInput{
		RequestValidationInput: requestInput,
		Status:                 status,
		Header:                 response.header,
		Body:                   http.NoBody,
		Options:                requestInput.Options,
	}).SetBodyBytes(response.body.Bytes())
	return openapi3filter.ValidateResponse(ctx, responseInput)
}

type bufferedResponseWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{header: make(http.Header)}
}

func (w *bufferedResponseWriter) Header() http.Header {
	return w.header
}

func (w *bufferedResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *bufferedResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(body)
}

func (w *bufferedResponseWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *bufferedResponseWriter) writeTo(target http.ResponseWriter) {
	for name, values := range w.header {
		target.Header()[name] = append([]string(nil), values...)
	}
	target.WriteHeader(w.statusCode())
	_, _ = target.Write(w.body.Bytes())
}
