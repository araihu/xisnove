package httpapi

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/domain"
)

func (s *Server) GetMonitorAvailabilityHistory(
	ctx context.Context,
	request GetMonitorAvailabilityHistoryRequestObject,
) (GetMonitorAvailabilityHistoryResponseObject, error) {
	if s.history == nil {
		return nil, fmt.Errorf("monitor history service is not configured")
	}
	var limit *int
	if request.Params.Limit != nil {
		value := int(*request.Params.Limit)
		limit = &value
	}
	view, err := s.history.GetMonitorAvailabilityHistory(
		ctx,
		domain.MonitorID(request.MonitorId.String()),
		request.Params.StartsAt,
		request.Params.EndsAt,
		limit,
	)
	if err != nil {
		problem, status, mapped := problemFromError(err)
		if mapped {
			return GetMonitorAvailabilityHistorydefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
		}
		return nil, err
	}
	monitorID, err := uuid.Parse(string(view.MonitorID))
	if err != nil {
		return nil, fmt.Errorf("map history monitor ID: %w", err)
	}
	samples := make([]MonitorAvailabilitySample, len(view.Samples))
	for index, sample := range view.Samples {
		id, err := uuid.Parse(sample.ID)
		if err != nil {
			return nil, fmt.Errorf("map history sample ID: %w", err)
		}
		locationID, err := uuid.Parse(string(sample.LocationID))
		if err != nil {
			return nil, fmt.Errorf("map history location ID: %w", err)
		}
		outcome := MonitorAvailabilitySampleOutcomeFailed
		if sample.Passed {
			outcome = MonitorAvailabilitySampleOutcomePassed
		}
		samples[index] = MonitorAvailabilitySample{
			Id: id, LocationId: locationID, ObservedAt: sample.At,
			Outcome: outcome, LatencyMillis: sample.Latency.Milliseconds(),
		}
	}
	return GetMonitorAvailabilityHistory200JSONResponse{
		MonitorId: monitorID, StartsAt: view.StartsAt, EndsAt: view.EndsAt,
		GeneratedAt: view.GeneratedAt, Samples: samples, Truncated: view.Truncated,
	}, nil
}
