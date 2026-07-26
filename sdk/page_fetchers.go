package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func decodePageResponse[T any](operation string) func(*http.Response, error) (Page[T], error) {
	return func(response *http.Response, err error) (Page[T], error) {
		if err != nil {
			return Page[T]{}, err
		}
		if response == nil {
			return Page[T]{}, fmt.Errorf("%s: %w", operation, ErrMissingHTTPResponse)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			return Page[T]{}, fmt.Errorf("%s: read response: %w", operation, readErr)
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return Page[T]{}, pageResponseError(operation, response, body)
		}
		var decoded struct {
			Items []T          `json:"items"`
			Page  PageMetadata `json:"page"`
		}
		if err := json.Unmarshal(body, &decoded); err != nil {
			return Page[T]{}, fmt.Errorf("%s: decode success response: %w", operation, err)
		}
		return Page[T]{Items: decoded.Items, NextCursor: pageCursor(decoded.Page)}, nil
	}
}

func (c *ClientWithResponses) AgentsPageFetcher(params ListAgentsParams, editors ...RequestEditorFn) PageFetcher[Agent] {
	return func(ctx context.Context, cursor string) (Page[Agent], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		return decodePageResponse[Agent]("list agents")(c.ListAgents(ctx, &request, editors...))
	}
}

func (c *ClientWithResponses) APITokensPageFetcher(params ListAPITokensParams, editors ...RequestEditorFn) PageFetcher[APIToken] {
	return func(ctx context.Context, cursor string) (Page[APIToken], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		return decodePageResponse[APIToken]("list API tokens")(c.ListAPITokens(ctx, &request, editors...))
	}
}

func (c *ClientWithResponses) DiscoveryCandidatesPageFetcher(params ListDiscoveryCandidatesParams, editors ...RequestEditorFn) PageFetcher[DiscoveryCandidate] {
	return func(ctx context.Context, cursor string) (Page[DiscoveryCandidate], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		return decodePageResponse[DiscoveryCandidate]("list discovery candidates")(c.ListDiscoveryCandidates(ctx, &request, editors...))
	}
}

func (c *ClientWithResponses) IncidentsPageFetcher(params ListIncidentsParams, editors ...RequestEditorFn) PageFetcher[Incident] {
	return func(ctx context.Context, cursor string) (Page[Incident], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		return decodePageResponse[Incident]("list incidents")(c.ListIncidents(ctx, &request, editors...))
	}
}

func (c *ClientWithResponses) IncidentEventsPageFetcher(incidentID IncidentID, params ListIncidentEventsParams, editors ...RequestEditorFn) PageFetcher[IncidentEvent] {
	return func(ctx context.Context, cursor string) (Page[IncidentEvent], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		return decodePageResponse[IncidentEvent]("list incident events")(c.ListIncidentEvents(ctx, incidentID, &request, editors...))
	}
}

func (c *ClientWithResponses) LocationsPageFetcher(params ListLocationsParams, editors ...RequestEditorFn) PageFetcher[Location] {
	return func(ctx context.Context, cursor string) (Page[Location], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		return decodePageResponse[Location]("list locations")(c.ListLocations(ctx, &request, editors...))
	}
}

func (c *ClientWithResponses) MaintenancePageFetcher(params ListMaintenanceParams, editors ...RequestEditorFn) PageFetcher[Maintenance] {
	return func(ctx context.Context, cursor string) (Page[Maintenance], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		return decodePageResponse[Maintenance]("list maintenance")(c.ListMaintenance(ctx, &request, editors...))
	}
}

func (c *ClientWithResponses) MonitorsPageFetcher(params ListMonitorsParams, editors ...RequestEditorFn) PageFetcher[Monitor] {
	return func(ctx context.Context, cursor string) (Page[Monitor], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		return decodePageResponse[Monitor]("list monitors")(c.ListMonitors(ctx, &request, editors...))
	}
}

func (c *ClientWithResponses) NotificationChannelsPageFetcher(params ListNotificationChannelsParams, editors ...RequestEditorFn) PageFetcher[NotificationChannel] {
	return func(ctx context.Context, cursor string) (Page[NotificationChannel], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		return decodePageResponse[NotificationChannel]("list notification channels")(c.ListNotificationChannels(ctx, &request, editors...))
	}
}

func (c *ClientWithResponses) NotificationDeliveriesPageFetcher(params ListNotificationDeliveriesParams, editors ...RequestEditorFn) PageFetcher[NotificationDelivery] {
	return func(ctx context.Context, cursor string) (Page[NotificationDelivery], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		return decodePageResponse[NotificationDelivery]("list notification deliveries")(c.ListNotificationDeliveries(ctx, &request, editors...))
	}
}

func (c *ClientWithResponses) NotificationRoutesPageFetcher(params ListNotificationRoutesParams, editors ...RequestEditorFn) PageFetcher[NotificationRoute] {
	return func(ctx context.Context, cursor string) (Page[NotificationRoute], error) {
		request := params
		request.Cursor = cursorPointer(cursor)
		return decodePageResponse[NotificationRoute]("list notification routes")(c.ListNotificationRoutes(ctx, &request, editors...))
	}
}
