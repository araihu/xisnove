package httpapi

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/domain"
)

func (s *Server) GetMonitorHealth(
	ctx context.Context,
	request GetMonitorHealthRequestObject,
) (GetMonitorHealthResponseObject, error) {
	view, err := s.health.GetMonitorHealth(
		ctx,
		domain.MonitorID(request.MonitorId.String()),
	)
	if err != nil {
		response, mapped := getMonitorHealthProblem(err)
		if mapped {
			return response, nil
		}
		return nil, err
	}
	monitorID, err := uuid.Parse(string(view.Monitor.MonitorID))
	if err != nil {
		return nil, fmt.Errorf("map health monitor ID: %w", err)
	}
	locations := make([]LocationHealth, len(view.Locations))
	for index, location := range view.Locations {
		locationID, err := uuid.Parse(string(location.LocationID))
		if err != nil {
			return nil, fmt.Errorf("map health location ID: %w", err)
		}
		locations[index] = LocationHealth{
			LocationId:           locationID,
			State:                HealthState(location.State),
			ConsecutiveFailures:  int32(location.ConsecutiveFailures),
			ConsecutiveSuccesses: int32(location.ConsecutiveSuccesses),
			LastObservedAt:       location.LastObservedAt,
		}
	}
	return GetMonitorHealth200JSONResponse{
		MonitorId: monitorID, State: HealthState(view.Monitor.State),
		LastTransitionAt: view.Monitor.LastTransitionAt, Locations: locations,
	}, nil
}

func (s *Server) GetActiveMonitorIncident(
	ctx context.Context,
	request GetActiveMonitorIncidentRequestObject,
) (GetActiveMonitorIncidentResponseObject, error) {
	incident, err := s.health.GetActiveIncident(
		ctx,
		domain.MonitorID(request.MonitorId.String()),
	)
	if err != nil {
		response, mapped := getActiveMonitorIncidentProblem(err)
		if mapped {
			return response, nil
		}
		return nil, err
	}
	if incident == nil {
		return GetActiveMonitorIncident204Response{}, nil
	}
	id, err := uuid.Parse(string(incident.ID))
	if err != nil {
		return nil, fmt.Errorf("map incident ID: %w", err)
	}
	monitorID, err := uuid.Parse(string(incident.MonitorID))
	if err != nil {
		return nil, fmt.Errorf("map incident monitor ID: %w", err)
	}
	return GetActiveMonitorIncident200JSONResponse{
		Id: id, MonitorId: monitorID, State: Open,
		Severity: IncidentSeverity(incident.Severity),
		OpenedAt: incident.OpenedAt, LastTransitionAt: incident.LastTransitionAt,
	}, nil
}

func getMonitorHealthProblem(err error) (GetMonitorHealthResponseObject, bool) {
	problem, status, ok := problemFromError(err)
	if !ok {
		return nil, false
	}
	return GetMonitorHealthdefaultApplicationProblemPlusJSONResponse{
		Body: problem, StatusCode: status,
	}, true
}

func getActiveMonitorIncidentProblem(
	err error,
) (GetActiveMonitorIncidentResponseObject, bool) {
	problem, status, ok := problemFromError(err)
	if !ok {
		return nil, false
	}
	return GetActiveMonitorIncidentdefaultApplicationProblemPlusJSONResponse{
		Body: problem, StatusCode: status,
	}, true
}
