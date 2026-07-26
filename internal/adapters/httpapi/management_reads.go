package httpapi

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

func (s *Server) ListLocations(ctx context.Context, request ListLocationsRequestObject) (ListLocationsResponseObject, error) {
	page, err := s.management.ListLocations(ctx, managementPageRequest(request.Params.Limit, request.Params.Cursor))
	if err != nil {
		problem, status, mapped := problemFromError(err)
		if mapped {
			return ListLocationsdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
		}
		return nil, err
	}
	items := make([]Location, len(page.Items))
	for index, item := range page.Items {
		items[index], err = mapLocation(item)
		if err != nil {
			return nil, err
		}
	}
	return ListLocations200JSONResponse{Items: items, Page: mapPageMetadata(page.NextCursor)}, nil
}

func (s *Server) GetLocation(ctx context.Context, request GetLocationRequestObject) (GetLocationResponseObject, error) {
	location, err := s.management.GetLocation(ctx, domain.LocationID(request.LocationId.String()))
	if err != nil {
		problem, status, mapped := problemFromError(err)
		if mapped {
			return GetLocationdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
		}
		return nil, err
	}
	mapped, err := mapLocation(location)
	if err != nil {
		return nil, err
	}
	return GetLocation200JSONResponse(mapped), nil
}

func (s *Server) ListMonitors(ctx context.Context, request ListMonitorsRequestObject) (ListMonitorsResponseObject, error) {
	page, err := s.management.ListMonitors(ctx, managementPageRequest(request.Params.Limit, request.Params.Cursor))
	if err != nil {
		problem, status, mapped := problemFromError(err)
		if mapped {
			return ListMonitorsdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
		}
		return nil, err
	}
	items := make([]Monitor, len(page.Items))
	for index, item := range page.Items {
		items[index], err = mapMonitor(item)
		if err != nil {
			return nil, err
		}
	}
	return ListMonitors200JSONResponse{Items: items, Page: mapPageMetadata(page.NextCursor)}, nil
}

func (s *Server) ListAgents(ctx context.Context, request ListAgentsRequestObject) (ListAgentsResponseObject, error) {
	page, err := s.management.ListAgents(ctx, managementPageRequest(request.Params.Limit, request.Params.Cursor))
	if err != nil {
		problem, status, mapped := problemFromError(err)
		if mapped {
			return ListAgentsdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
		}
		return nil, err
	}
	items := make([]Agent, len(page.Items))
	for index, item := range page.Items {
		items[index], err = mapManagementAgent(item)
		if err != nil {
			return nil, err
		}
	}
	return ListAgents200JSONResponse{Items: items, Page: mapPageMetadata(page.NextCursor)}, nil
}

func (s *Server) GetAgent(ctx context.Context, request GetAgentRequestObject) (GetAgentResponseObject, error) {
	agent, err := s.management.GetAgent(ctx, domain.AgentID(request.AgentId.String()))
	if err != nil {
		problem, status, mapped := problemFromError(err)
		if mapped {
			return GetAgentdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
		}
		return nil, err
	}
	mapped, err := mapManagementAgent(agent)
	if err != nil {
		return nil, err
	}
	return GetAgent200JSONResponse(mapped), nil
}

func (s *Server) ListIncidents(ctx context.Context, request ListIncidentsRequestObject) (ListIncidentsResponseObject, error) {
	resolution := port.IncidentResolutionAll
	if request.Params.State != nil {
		resolution = port.IncidentResolutionFilter(*request.Params.State)
	}
	page, err := s.management.ListIncidents(ctx, resolution, managementPageRequest(request.Params.Limit, request.Params.Cursor))
	if err != nil {
		problem, status, mapped := problemFromError(err)
		if mapped {
			return ListIncidentsdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
		}
		return nil, err
	}
	items := make([]Incident, len(page.Items))
	for index, item := range page.Items {
		items[index], err = mapManagementIncident(item)
		if err != nil {
			return nil, err
		}
	}
	return ListIncidents200JSONResponse{Items: items, Page: mapPageMetadata(page.NextCursor)}, nil
}

func (s *Server) GetIncident(ctx context.Context, request GetIncidentRequestObject) (GetIncidentResponseObject, error) {
	incident, err := s.management.GetIncident(ctx, domain.IncidentID(request.IncidentId.String()))
	if err != nil {
		problem, status, mapped := problemFromError(err)
		if mapped {
			return GetIncidentdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
		}
		return nil, err
	}
	mapped, err := mapManagementIncident(incident)
	if err != nil {
		return nil, err
	}
	return GetIncident200JSONResponse(mapped), nil
}

func (s *Server) ListIncidentEvents(ctx context.Context, request ListIncidentEventsRequestObject) (ListIncidentEventsResponseObject, error) {
	page, err := s.management.ListIncidentEvents(ctx, domain.IncidentID(request.IncidentId.String()), managementPageRequest(request.Params.Limit, request.Params.Cursor))
	if err != nil {
		problem, status, mapped := problemFromError(err)
		if mapped {
			return ListIncidentEventsdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
		}
		return nil, err
	}
	items := make([]IncidentEvent, len(page.Items))
	for index, item := range page.Items {
		items[index], err = mapManagementIncidentEvent(item)
		if err != nil {
			return nil, err
		}
	}
	return ListIncidentEvents200JSONResponse{Items: items, Page: mapPageMetadata(page.NextCursor)}, nil
}

func managementPageRequest(limit *Limit, cursor *Cursor) application.PageRequest {
	request := application.PageRequest{}
	if limit != nil {
		request.Limit = int(*limit)
	}
	if cursor != nil {
		request.Cursor = string(*cursor)
	}
	return request
}

func mapPageMetadata(cursor string) PageMetadata {
	metadata := PageMetadata{}
	if cursor != "" {
		metadata.NextCursor = &cursor
	}
	return metadata
}

func mapManagementAgent(agent domain.Agent) (Agent, error) {
	id, err := uuid.Parse(string(agent.ID))
	if err != nil {
		return Agent{}, fmt.Errorf("map agent ID: %w", err)
	}
	locationID, err := uuid.Parse(string(agent.LocationID))
	if err != nil {
		return Agent{}, fmt.Errorf("map agent location ID: %w", err)
	}
	capabilities := make([]AgentCapability, len(agent.Capabilities))
	for index, capability := range agent.Capabilities {
		capabilities[index] = AgentCapability(capability)
	}
	mapped := Agent{
		Id: id, LocationId: locationID, Name: agent.Name,
		Enabled: agent.RevokedAt == nil, CredentialGeneration: int64(agent.CredentialGeneration),
		Capabilities: capabilities, CreatedAt: agent.CreatedAt, UpdatedAt: agent.UpdatedAt,
	}
	if agent.Version != "" {
		mapped.Version = &agent.Version
	}
	if !agent.LastSeenAt.IsZero() {
		lastSeen := agent.LastSeenAt.UTC()
		mapped.LastSeenAt = &lastSeen
	}
	return mapped, nil
}

func mapManagementIncident(incident domain.Incident) (Incident, error) {
	id, err := uuid.Parse(string(incident.ID))
	if err != nil {
		return Incident{}, fmt.Errorf("map incident ID: %w", err)
	}
	monitorID, err := uuid.Parse(string(incident.MonitorID))
	if err != nil {
		return Incident{}, fmt.Errorf("map incident monitor ID: %w", err)
	}
	state := IncidentStateOpen
	if incident.RecoveredAt != nil {
		state = IncidentStateResolved
	}
	return Incident{
		Id: id, MonitorId: monitorID, State: state, Severity: IncidentSeverity(incident.Severity),
		OpenedAt: incident.OpenedAt, ResolvedAt: incident.RecoveredAt, LastTransitionAt: incident.LastTransitionAt,
	}, nil
}

func mapManagementIncidentEvent(event domain.IncidentEvent) (IncidentEvent, error) {
	id, err := uuid.Parse(event.ID)
	if err != nil {
		return IncidentEvent{}, fmt.Errorf("map incident event ID: %w", err)
	}
	incidentID, err := uuid.Parse(string(event.IncidentID))
	if err != nil {
		return IncidentEvent{}, fmt.Errorf("map incident event incident ID: %w", err)
	}
	return IncidentEvent{
		Id: id, IncidentId: incidentID, Action: NotificationAction(event.Action),
		PreviousState: HealthState(event.PreviousState), State: HealthState(event.State),
		Severity: IncidentSeverity(event.Severity), OccurredAt: event.CreatedAt,
	}, nil
}
