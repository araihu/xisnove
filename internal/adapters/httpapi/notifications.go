package httpapi

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

func (s *Server) CreateNotificationChannel(ctx context.Context, request CreateNotificationChannelRequestObject) (CreateNotificationChannelResponseObject, error) {
	if request.Body == nil {
		return createNotificationChannelProblem(requiredBody()), nil
	}
	command, err := notificationChannelCommand(request.Body.Name, request.Body.Enabled, request.Body.Configuration)
	if err != nil {
		return createNotificationChannelProblem(err), nil
	}
	channel, err := s.notifications.CreateChannel(ctx, command)
	if err != nil {
		if response, ok := notificationProblem(err); ok {
			return CreateNotificationChanneldefaultApplicationProblemPlusJSONResponse(response), nil
		}
		return nil, err
	}
	return CreateNotificationChannel201JSONResponse(mapNotificationChannel(channel)), nil
}

func (s *Server) ListNotificationChannels(ctx context.Context, request ListNotificationChannelsRequestObject) (ListNotificationChannelsResponseObject, error) {
	limit, offset := pageValues(request.Params.Limit, request.Params.Offset)
	channels, err := s.notifications.ListChannels(ctx, limit, offset)
	if err != nil {
		if response, ok := notificationProblem(err); ok {
			return ListNotificationChannelsdefaultApplicationProblemPlusJSONResponse(response), nil
		}
		return nil, err
	}
	items := make([]NotificationChannel, len(channels))
	for index := range channels {
		items[index] = mapNotificationChannel(channels[index])
	}
	return ListNotificationChannels200JSONResponse{
		Items: items, Page: PageMetadata{}, Limit: int32(limit), Offset: int32(offset),
	}, nil
}

func (s *Server) GetNotificationChannel(ctx context.Context, request GetNotificationChannelRequestObject) (GetNotificationChannelResponseObject, error) {
	channel, err := s.notifications.GetChannel(ctx, domain.NotificationChannelID(request.ChannelId.String()))
	if err != nil {
		if response, ok := notificationProblem(err); ok {
			return GetNotificationChanneldefaultApplicationProblemPlusJSONResponse(response), nil
		}
		return nil, err
	}
	return GetNotificationChannel200JSONResponse(mapNotificationChannel(channel)), nil
}

func (s *Server) UpdateNotificationChannel(ctx context.Context, request UpdateNotificationChannelRequestObject) (UpdateNotificationChannelResponseObject, error) {
	if request.Body == nil {
		return updateNotificationChannelProblem(requiredBody()), nil
	}
	command, err := notificationChannelCommand(request.Body.Name, request.Body.Enabled, request.Body.Configuration)
	if err != nil {
		return updateNotificationChannelProblem(err), nil
	}
	channel, err := s.notifications.UpdateChannel(ctx, domain.NotificationChannelID(request.ChannelId.String()), command)
	if err != nil {
		if response, ok := notificationProblem(err); ok {
			return UpdateNotificationChanneldefaultApplicationProblemPlusJSONResponse(response), nil
		}
		return nil, err
	}
	return UpdateNotificationChannel200JSONResponse(mapNotificationChannel(channel)), nil
}

func (s *Server) DisableNotificationChannel(ctx context.Context, request DisableNotificationChannelRequestObject) (DisableNotificationChannelResponseObject, error) {
	if err := s.notifications.DisableChannel(ctx, domain.NotificationChannelID(request.ChannelId.String())); err != nil {
		if response, ok := notificationProblem(err); ok {
			return DisableNotificationChanneldefaultApplicationProblemPlusJSONResponse(response), nil
		}
		return nil, err
	}
	return DisableNotificationChannel204Response{}, nil
}

func notificationChannelCommand(name string, enabled bool, input NotificationChannelConfigurationInput) (application.PutNotificationChannelCommand, error) {
	discriminator, err := input.Discriminator()
	if err != nil {
		return application.PutNotificationChannelCommand{}, &application.ValidationError{Fields: map[string]string{"configuration.kind": "is required"}}
	}
	config := application.NotificationChannelConfig{Kind: domain.NotificationChannelKind(discriminator)}
	switch discriminator {
	case string(domain.NotificationChannelShoutrrr):
		value, err := input.AsShoutrrrChannelConfigurationInput()
		if err != nil || value.ServiceUrl == nil {
			return application.PutNotificationChannelCommand{}, &application.ValidationError{Fields: map[string]string{"configuration.serviceUrl": "is required"}}
		}
		config.ShoutrrrServiceURL = *value.ServiceUrl
	case string(domain.NotificationChannelAlertmanager):
		value, err := input.AsAlertmanagerChannelConfigurationInput()
		if err != nil {
			return application.PutNotificationChannelCommand{}, &application.ValidationError{Fields: map[string]string{"configuration": "is invalid"}}
		}
		config.AlertmanagerURL = value.Endpoint
		if value.BearerToken != nil {
			config.BearerToken = *value.BearerToken
		}
	default:
		return application.PutNotificationChannelCommand{}, &application.ValidationError{Fields: map[string]string{"configuration.kind": "is unsupported"}}
	}
	return application.PutNotificationChannelCommand{Name: name, Enabled: enabled, Config: config}, nil
}

func mapNotificationChannel(channel domain.NotificationChannel) NotificationChannel {
	return NotificationChannel{
		Id: uuid.MustParse(string(channel.ID)), Name: channel.Name,
		Kind: NotificationChannelKind(channel.Kind), Enabled: channel.Enabled,
		CreatedAt: channel.CreatedAt, UpdatedAt: channel.UpdatedAt,
	}
}

func (s *Server) CreateNotificationRoute(ctx context.Context, request CreateNotificationRouteRequestObject) (CreateNotificationRouteResponseObject, error) {
	if request.Body == nil {
		return createNotificationRouteProblem(requiredBody()), nil
	}
	route, err := s.notifications.CreateRoute(ctx, routeCommand(*request.Body))
	if err != nil {
		if response, ok := notificationProblem(err); ok {
			return CreateNotificationRoutedefaultApplicationProblemPlusJSONResponse(response), nil
		}
		return nil, err
	}
	mapped, err := mapNotificationRoute(route)
	if err != nil {
		return nil, err
	}
	return CreateNotificationRoute201JSONResponse(mapped), nil
}

func (s *Server) ListNotificationRoutes(ctx context.Context, request ListNotificationRoutesRequestObject) (ListNotificationRoutesResponseObject, error) {
	limit, offset := pageValues(request.Params.Limit, request.Params.Offset)
	routes, err := s.notifications.ListRoutes(ctx, limit, offset)
	if err != nil {
		if response, ok := notificationProblem(err); ok {
			return ListNotificationRoutesdefaultApplicationProblemPlusJSONResponse(response), nil
		}
		return nil, err
	}
	items := make([]NotificationRoute, len(routes))
	for index := range routes {
		items[index], err = mapNotificationRoute(routes[index])
		if err != nil {
			return nil, err
		}
	}
	return ListNotificationRoutes200JSONResponse{
		Items: items, Page: PageMetadata{}, Limit: int32(limit), Offset: int32(offset),
	}, nil
}

func (s *Server) GetNotificationRoute(ctx context.Context, request GetNotificationRouteRequestObject) (GetNotificationRouteResponseObject, error) {
	route, err := s.notifications.GetRoute(ctx, domain.NotificationRouteID(request.RouteId.String()))
	if err != nil {
		if response, ok := notificationProblem(err); ok {
			return GetNotificationRoutedefaultApplicationProblemPlusJSONResponse(response), nil
		}
		return nil, err
	}
	mapped, err := mapNotificationRoute(route)
	if err != nil {
		return nil, err
	}
	return GetNotificationRoute200JSONResponse(mapped), nil
}

func (s *Server) UpdateNotificationRoute(ctx context.Context, request UpdateNotificationRouteRequestObject) (UpdateNotificationRouteResponseObject, error) {
	if request.Body == nil {
		return updateNotificationRouteProblem(requiredBody()), nil
	}
	route, err := s.notifications.UpdateRoute(ctx, domain.NotificationRouteID(request.RouteId.String()), routeCommand(*request.Body))
	if err != nil {
		if response, ok := notificationProblem(err); ok {
			return UpdateNotificationRoutedefaultApplicationProblemPlusJSONResponse(response), nil
		}
		return nil, err
	}
	mapped, err := mapNotificationRoute(route)
	if err != nil {
		return nil, err
	}
	return UpdateNotificationRoute200JSONResponse(mapped), nil
}

func (s *Server) DisableNotificationRoute(ctx context.Context, request DisableNotificationRouteRequestObject) (DisableNotificationRouteResponseObject, error) {
	if err := s.notifications.DisableRoute(ctx, domain.NotificationRouteID(request.RouteId.String())); err != nil {
		if response, ok := notificationProblem(err); ok {
			return DisableNotificationRoutedefaultApplicationProblemPlusJSONResponse(response), nil
		}
		return nil, err
	}
	return DisableNotificationRoute204Response{}, nil
}

func routeCommand(input NotificationRouteInput) application.PutNotificationRouteCommand {
	var monitorID *domain.MonitorID
	if input.MonitorId != nil {
		value := domain.MonitorID(input.MonitorId.String())
		monitorID = &value
	}
	actions := make([]domain.NotificationAction, len(input.Actions))
	for index := range input.Actions {
		actions[index] = domain.NotificationAction(input.Actions[index])
	}
	severities := make([]domain.IncidentSeverity, len(input.Severities))
	for index := range input.Severities {
		severities[index] = domain.IncidentSeverity(input.Severities[index])
	}
	return application.PutNotificationRouteCommand{
		Name: input.Name, ChannelID: domain.NotificationChannelID(input.ChannelId.String()),
		MonitorID: monitorID, LabelMatchers: input.LabelMatchers,
		Actions: actions, Severities: severities, Template: input.Template,
		Enabled: input.Enabled, Precedence: input.Precedence,
	}
}

func mapNotificationRoute(route domain.NotificationRoute) (NotificationRoute, error) {
	id, err := uuid.Parse(string(route.ID))
	if err != nil {
		return NotificationRoute{}, fmt.Errorf("map notification route ID: %w", err)
	}
	channelID, err := uuid.Parse(string(route.ChannelID))
	if err != nil {
		return NotificationRoute{}, fmt.Errorf("map notification channel ID: %w", err)
	}
	var monitorID *uuid.UUID
	if route.MonitorID != nil {
		value, err := uuid.Parse(string(*route.MonitorID))
		if err != nil {
			return NotificationRoute{}, fmt.Errorf("map notification monitor ID: %w", err)
		}
		monitorID = &value
	}
	actions := make([]NotificationAction, len(route.Actions))
	for index := range route.Actions {
		actions[index] = NotificationAction(route.Actions[index])
	}
	severities := make([]IncidentSeverity, len(route.Severities))
	for index := range route.Severities {
		severities[index] = IncidentSeverity(route.Severities[index])
	}
	return NotificationRoute{
		Id: id, Name: route.Name, ChannelId: channelID, MonitorId: monitorID,
		LabelMatchers: route.LabelMatchers, Actions: actions, Severities: severities,
		Template: route.Template, Enabled: route.Enabled, Precedence: route.Precedence,
		CreatedAt: route.CreatedAt, UpdatedAt: route.UpdatedAt,
	}, nil
}

func (s *Server) ListNotificationDeliveries(ctx context.Context, request ListNotificationDeliveriesRequestObject) (ListNotificationDeliveriesResponseObject, error) {
	limit, offset := pageValues(request.Params.Limit, request.Params.Offset)
	records, err := s.notifications.ListDeliveries(ctx, limit, offset)
	if err != nil {
		if response, ok := notificationProblem(err); ok {
			return ListNotificationDeliveriesdefaultApplicationProblemPlusJSONResponse(response), nil
		}
		return nil, err
	}
	items := make([]NotificationDelivery, len(records))
	for index := range records {
		items[index], err = mapNotificationDelivery(records[index])
		if err != nil {
			return nil, err
		}
	}
	return ListNotificationDeliveries200JSONResponse{
		Items: items, Page: PageMetadata{}, Limit: int32(limit), Offset: int32(offset),
	}, nil
}

func (s *Server) GetNotificationDelivery(ctx context.Context, request GetNotificationDeliveryRequestObject) (GetNotificationDeliveryResponseObject, error) {
	detail, err := s.notifications.GetDelivery(ctx, domain.NotificationDeliveryID(request.DeliveryId.String()))
	if err != nil {
		if response, ok := notificationProblem(err); ok {
			return GetNotificationDeliverydefaultApplicationProblemPlusJSONResponse(response), nil
		}
		return nil, err
	}
	delivery, err := mapNotificationDelivery(detail.Delivery)
	if err != nil {
		return nil, err
	}
	attempts := make([]NotificationDeliveryAttempt, len(detail.Attempts))
	for index := range detail.Attempts {
		attempts[index], err = mapNotificationAttempt(detail.Attempts[index])
		if err != nil {
			return nil, err
		}
	}
	return GetNotificationDelivery200JSONResponse{Delivery: delivery, Attempts: attempts}, nil
}

func (s *Server) ReplayNotificationDelivery(ctx context.Context, request ReplayNotificationDeliveryRequestObject) (ReplayNotificationDeliveryResponseObject, error) {
	if err := s.notifications.ReplayDelivery(ctx, domain.NotificationDeliveryID(request.DeliveryId.String())); err != nil {
		if response, ok := notificationProblem(err); ok {
			return ReplayNotificationDeliverydefaultApplicationProblemPlusJSONResponse(response), nil
		}
		return nil, err
	}
	return ReplayNotificationDelivery202Response{}, nil
}

func mapNotificationDelivery(record port.NotificationOutboxRecord) (NotificationDelivery, error) {
	id, err := uuid.Parse(string(record.ID))
	if err != nil {
		return NotificationDelivery{}, fmt.Errorf("map notification delivery ID: %w", err)
	}
	routeID, err := uuid.Parse(string(record.RouteID))
	if err != nil {
		return NotificationDelivery{}, fmt.Errorf("map notification delivery route ID: %w", err)
	}
	channelID, err := uuid.Parse(string(record.ChannelID))
	if err != nil {
		return NotificationDelivery{}, fmt.Errorf("map notification delivery channel ID: %w", err)
	}
	snapshot, err := mapRenderSnapshot(record.RenderSnapshot)
	if err != nil {
		return NotificationDelivery{}, err
	}
	lastError, lastDiagnostic := optionalString(record.LastErrorClass), optionalString(record.LastDiagnostic)
	return NotificationDelivery{
		Id: id, RouteId: routeID, ChannelId: channelID,
		State: NotificationDeliveryState(record.State), AvailableAt: record.AvailableAt,
		AttemptCount: int32(record.AttemptCount), LastErrorClass: lastError,
		LastDiagnostic: lastDiagnostic, DeliveredAt: record.DeliveredAt,
		SuppressedAt: record.SuppressedAt, RenderSnapshot: snapshot,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}, nil
}

func mapRenderSnapshot(snapshot domain.RenderSnapshot) (NotificationRenderSnapshot, error) {
	eventID, err := uuid.Parse(snapshot.EventID)
	if err != nil {
		return NotificationRenderSnapshot{}, fmt.Errorf("map notification event ID: %w", err)
	}
	incidentID, err := uuid.Parse(string(snapshot.IncidentID))
	if err != nil {
		return NotificationRenderSnapshot{}, fmt.Errorf("map notification incident ID: %w", err)
	}
	monitorID, err := uuid.Parse(string(snapshot.MonitorID))
	if err != nil {
		return NotificationRenderSnapshot{}, fmt.Errorf("map notification monitor ID: %w", err)
	}
	routeID, err := uuid.Parse(string(snapshot.RouteID))
	if err != nil {
		return NotificationRenderSnapshot{}, fmt.Errorf("map notification route ID: %w", err)
	}
	channelID, err := uuid.Parse(string(snapshot.ChannelID))
	if err != nil {
		return NotificationRenderSnapshot{}, fmt.Errorf("map notification channel ID: %w", err)
	}
	return NotificationRenderSnapshot{
		EventId: eventID, Action: NotificationAction(snapshot.Action), IncidentId: incidentID,
		MonitorId: monitorID, MonitorName: snapshot.MonitorName,
		MonitorDescription: snapshot.MonitorDescription, MonitorLabels: snapshot.MonitorLabels,
		PreviousState: HealthState(snapshot.PreviousState), State: HealthState(snapshot.State),
		Severity: IncidentSeverity(snapshot.Severity), OccurredAt: snapshot.OccurredAt,
		RouteId: routeID, ChannelId: channelID,
		ChannelKind: NotificationRenderSnapshotChannelKind(snapshot.ChannelKind),
		Template:    snapshot.Template, RouteUpdatedAt: snapshot.RouteUpdatedAt,
	}, nil
}

func mapNotificationAttempt(record port.NotificationDeliveryAttemptRecord) (NotificationDeliveryAttempt, error) {
	id, err := uuid.Parse(record.ID)
	if err != nil {
		return NotificationDeliveryAttempt{}, fmt.Errorf("map notification attempt ID: %w", err)
	}
	return NotificationDeliveryAttempt{
		Id: id, Ordinal: int32(record.Ordinal), StartedAt: record.StartedAt,
		FinishedAt: record.FinishedAt, Outcome: NotificationDeliveryAttemptOutcome(record.Outcome),
		ErrorClass: optionalString(record.ErrorClass), Diagnostic: optionalString(record.Diagnostic),
		ProviderReceipt: optionalString(record.ProviderReceipt),
	}, nil
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func pageValues(limit, offset *int32) (int, int) {
	resultLimit, resultOffset := 50, 0
	if limit != nil {
		resultLimit = int(*limit)
	}
	if offset != nil {
		resultOffset = int(*offset)
	}
	if resultLimit <= 0 {
		resultLimit = 50
	}
	if resultLimit > application.MaxPageLimit {
		resultLimit = application.MaxPageLimit
	}
	if resultOffset < 0 {
		resultOffset = 0
	}
	return resultLimit, resultOffset
}

func requiredBody() error {
	return &application.ValidationError{Fields: map[string]string{"body": "is required"}}
}

type notificationProblemResponse struct {
	Body       Problem
	StatusCode int
}

func notificationProblem(err error) (notificationProblemResponse, bool) {
	body, status, ok := problemFromError(err)
	return notificationProblemResponse{Body: body, StatusCode: status}, ok
}

func createNotificationChannelProblem(err error) CreateNotificationChannelResponseObject {
	response, _ := notificationProblem(err)
	return CreateNotificationChanneldefaultApplicationProblemPlusJSONResponse(response)
}

func updateNotificationChannelProblem(err error) UpdateNotificationChannelResponseObject {
	response, _ := notificationProblem(err)
	return UpdateNotificationChanneldefaultApplicationProblemPlusJSONResponse(response)
}

func createNotificationRouteProblem(err error) CreateNotificationRouteResponseObject {
	response, _ := notificationProblem(err)
	return CreateNotificationRoutedefaultApplicationProblemPlusJSONResponse(response)
}

func updateNotificationRouteProblem(err error) UpdateNotificationRouteResponseObject {
	response, _ := notificationProblem(err)
	return UpdateNotificationRoutedefaultApplicationProblemPlusJSONResponse(response)
}
