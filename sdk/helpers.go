package sdk

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

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
