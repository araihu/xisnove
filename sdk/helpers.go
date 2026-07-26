package sdk

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

var (
	ErrEmptyBearerToken    = errors.New("bearer token is empty")
	ErrEmptyIdempotencyKey = errors.New("idempotency key is empty")
)

// Health-state aliases preserve the original SDK surface after additional
// OpenAPI enums required the generator to prefix its generated constants.
const (
	Pending  = HealthStatePending
	Up       = HealthStateUp
	Down     = HealthStateDown
	Degraded = HealthStateDegraded
	Unknown  = HealthStateUnknown
)

func WithBearerToken(token string) RequestEditorFn {
	return func(_ context.Context, request *http.Request) error {
		if token == "" {
			return ErrEmptyBearerToken
		}
		request.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
}

func WithIdempotencyKey(key string) RequestEditorFn {
	return func(_ context.Context, request *http.Request) error {
		if key == "" {
			return ErrEmptyIdempotencyKey
		}
		request.Header.Set("Idempotency-Key", key)
		return nil
	}
}

func (c *ClientWithResponses) RequireMonitor(
	ctx context.Context,
	monitorID string,
	reqEditors ...RequestEditorFn,
) (*Monitor, error) {
	id, err := uuid.Parse(monitorID)
	if err != nil {
		return nil, fmt.Errorf("get monitor: invalid monitor ID: %w", err)
	}

	response, err := c.GetMonitorWithResponse(ctx, id, reqEditors...)
	if err != nil {
		return nil, err
	}
	if response.JSON200 == nil {
		return nil, fmt.Errorf("get monitor: HTTP %d", response.StatusCode())
	}
	return response.JSON200, nil
}
