package sdk

import (
	"context"
	"fmt"
	"net/http"
)

func cursorPointer(value string) *Cursor {
	if value == "" {
		return nil
	}
	cursor := Cursor(value)
	return &cursor
}

func pageCursor(metadata PageMetadata) string {
	if metadata.NextCursor == nil {
		return ""
	}
	return *metadata.NextCursor
}

func pageResponseError(operation string, response *http.Response, body []byte) error {
	err := ErrorFromResponse(response, body)
	if err == nil {
		return fmt.Errorf("%s: response has no success body", operation)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func (c *ClientWithResponses) AgentsPageFetcher(params ListAgentsParams, editors ...RequestEditorFn) PageFetcher[Agent] {
	return func(ctx context.Context, cursor string) (Page[Agent], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		response, err := c.ListAgentsWithResponse(ctx, &request, editors...)
		if err != nil {
			return Page[Agent]{}, err
		}
		if response.JSON200 == nil {
			return Page[Agent]{}, pageResponseError("list agents", response.HTTPResponse, response.Body)
		}
		return Page[Agent]{Items: response.JSON200.Items, NextCursor: pageCursor(response.JSON200.Page)}, nil
	}
}

func (c *ClientWithResponses) APITokensPageFetcher(params ListAPITokensParams, editors ...RequestEditorFn) PageFetcher[APIToken] {
	return func(ctx context.Context, cursor string) (Page[APIToken], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		response, err := c.ListAPITokensWithResponse(ctx, &request, editors...)
		if err != nil {
			return Page[APIToken]{}, err
		}
		if response.JSON200 == nil {
			return Page[APIToken]{}, pageResponseError("list API tokens", response.HTTPResponse, response.Body)
		}
		return Page[APIToken]{Items: response.JSON200.Items, NextCursor: pageCursor(response.JSON200.Page)}, nil
	}
}

func (c *ClientWithResponses) DiscoveryCandidatesPageFetcher(params ListDiscoveryCandidatesParams, editors ...RequestEditorFn) PageFetcher[DiscoveryCandidate] {
	return func(ctx context.Context, cursor string) (Page[DiscoveryCandidate], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		response, err := c.ListDiscoveryCandidatesWithResponse(ctx, &request, editors...)
		if err != nil {
			return Page[DiscoveryCandidate]{}, err
		}
		if response.JSON200 == nil {
			return Page[DiscoveryCandidate]{}, pageResponseError("list discovery candidates", response.HTTPResponse, response.Body)
		}
		return Page[DiscoveryCandidate]{Items: response.JSON200.Items, NextCursor: pageCursor(response.JSON200.Page)}, nil
	}
}

func (c *ClientWithResponses) IncidentsPageFetcher(params ListIncidentsParams, editors ...RequestEditorFn) PageFetcher[Incident] {
	return func(ctx context.Context, cursor string) (Page[Incident], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		response, err := c.ListIncidentsWithResponse(ctx, &request, editors...)
		if err != nil {
			return Page[Incident]{}, err
		}
		if response.JSON200 == nil {
			return Page[Incident]{}, pageResponseError("list incidents", response.HTTPResponse, response.Body)
		}
		return Page[Incident]{Items: response.JSON200.Items, NextCursor: pageCursor(response.JSON200.Page)}, nil
	}
}

func (c *ClientWithResponses) IncidentEventsPageFetcher(incidentID IncidentID, params ListIncidentEventsParams, editors ...RequestEditorFn) PageFetcher[IncidentEvent] {
	return func(ctx context.Context, cursor string) (Page[IncidentEvent], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		response, err := c.ListIncidentEventsWithResponse(ctx, incidentID, &request, editors...)
		if err != nil {
			return Page[IncidentEvent]{}, err
		}
		if response.JSON200 == nil {
			return Page[IncidentEvent]{}, pageResponseError("list incident events", response.HTTPResponse, response.Body)
		}
		return Page[IncidentEvent]{Items: response.JSON200.Items, NextCursor: pageCursor(response.JSON200.Page)}, nil
	}
}

func (c *ClientWithResponses) LocationsPageFetcher(params ListLocationsParams, editors ...RequestEditorFn) PageFetcher[Location] {
	return func(ctx context.Context, cursor string) (Page[Location], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		response, err := c.ListLocationsWithResponse(ctx, &request, editors...)
		if err != nil {
			return Page[Location]{}, err
		}
		if response.JSON200 == nil {
			return Page[Location]{}, pageResponseError("list locations", response.HTTPResponse, response.Body)
		}
		return Page[Location]{Items: response.JSON200.Items, NextCursor: pageCursor(response.JSON200.Page)}, nil
	}
}

func (c *ClientWithResponses) MaintenancePageFetcher(params ListMaintenanceParams, editors ...RequestEditorFn) PageFetcher[Maintenance] {
	return func(ctx context.Context, cursor string) (Page[Maintenance], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		response, err := c.ListMaintenanceWithResponse(ctx, &request, editors...)
		if err != nil {
			return Page[Maintenance]{}, err
		}
		if response.JSON200 == nil {
			return Page[Maintenance]{}, pageResponseError("list maintenance", response.HTTPResponse, response.Body)
		}
		return Page[Maintenance]{Items: response.JSON200.Items, NextCursor: pageCursor(response.JSON200.Page)}, nil
	}
}

func (c *ClientWithResponses) MonitorsPageFetcher(params ListMonitorsParams, editors ...RequestEditorFn) PageFetcher[Monitor] {
	return func(ctx context.Context, cursor string) (Page[Monitor], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		response, err := c.ListMonitorsWithResponse(ctx, &request, editors...)
		if err != nil {
			return Page[Monitor]{}, err
		}
		if response.JSON200 == nil {
			return Page[Monitor]{}, pageResponseError("list monitors", response.HTTPResponse, response.Body)
		}
		return Page[Monitor]{Items: response.JSON200.Items, NextCursor: pageCursor(response.JSON200.Page)}, nil
	}
}

func (c *ClientWithResponses) NotificationChannelsPageFetcher(params ListNotificationChannelsParams, editors ...RequestEditorFn) PageFetcher[NotificationChannel] {
	return func(ctx context.Context, cursor string) (Page[NotificationChannel], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		response, err := c.ListNotificationChannelsWithResponse(ctx, &request, editors...)
		if err != nil {
			return Page[NotificationChannel]{}, err
		}
		if response.JSON200 == nil {
			return Page[NotificationChannel]{}, pageResponseError("list notification channels", response.HTTPResponse, response.Body)
		}
		return Page[NotificationChannel]{Items: response.JSON200.Items, NextCursor: pageCursor(response.JSON200.Page)}, nil
	}
}

func (c *ClientWithResponses) NotificationDeliveriesPageFetcher(params ListNotificationDeliveriesParams, editors ...RequestEditorFn) PageFetcher[NotificationDelivery] {
	return func(ctx context.Context, cursor string) (Page[NotificationDelivery], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		response, err := c.ListNotificationDeliveriesWithResponse(ctx, &request, editors...)
		if err != nil {
			return Page[NotificationDelivery]{}, err
		}
		if response.JSON200 == nil {
			return Page[NotificationDelivery]{}, pageResponseError("list notification deliveries", response.HTTPResponse, response.Body)
		}
		return Page[NotificationDelivery]{Items: response.JSON200.Items, NextCursor: pageCursor(response.JSON200.Page)}, nil
	}
}

func (c *ClientWithResponses) NotificationRoutesPageFetcher(params ListNotificationRoutesParams, editors ...RequestEditorFn) PageFetcher[NotificationRoute] {
	return func(ctx context.Context, cursor string) (Page[NotificationRoute], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		response, err := c.ListNotificationRoutesWithResponse(ctx, &request, editors...)
		if err != nil {
			return Page[NotificationRoute]{}, err
		}
		if response.JSON200 == nil {
			return Page[NotificationRoute]{}, pageResponseError("list notification routes", response.HTTPResponse, response.Body)
		}
		return Page[NotificationRoute]{Items: response.JSON200.Items, NextCursor: pageCursor(response.JSON200.Page)}, nil
	}
}
