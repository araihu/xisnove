package httpapi

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

func (s *Server) CreateMaintenance(ctx context.Context, request CreateMaintenanceRequestObject) (CreateMaintenanceResponseObject, error) {
	if request.Body == nil {
		response, _ := notificationProblem(requiredBody())
		return CreateMaintenancedefaultApplicationProblemPlusJSONResponse(response), nil
	}
	record, err := s.notifications.CreateMaintenance(ctx, application.CreateMaintenanceCommand{
		MonitorID: domain.MonitorID(request.Body.MonitorId.String()),
		StartsAt:  request.Body.StartsAt, EndsAt: request.Body.EndsAt, Reason: request.Body.Reason,
	})
	if err != nil {
		if response, ok := notificationProblem(err); ok {
			return CreateMaintenancedefaultApplicationProblemPlusJSONResponse(response), nil
		}
		return nil, err
	}
	mapped, err := mapMaintenance(record)
	if err != nil {
		return nil, err
	}
	return CreateMaintenance201JSONResponse(mapped), nil
}

func (s *Server) ListMaintenance(ctx context.Context, request ListMaintenanceRequestObject) (ListMaintenanceResponseObject, error) {
	limit, offset := pageValues(request.Params.Limit, request.Params.Offset)
	records, err := s.notifications.ListMaintenance(ctx, limit, offset)
	if err != nil {
		if response, ok := notificationProblem(err); ok {
			return ListMaintenancedefaultApplicationProblemPlusJSONResponse(response), nil
		}
		return nil, err
	}
	items := make([]Maintenance, len(records))
	for index := range records {
		items[index], err = mapMaintenance(records[index])
		if err != nil {
			return nil, err
		}
	}
	return ListMaintenance200JSONResponse{Items: items, Limit: int32(limit), Offset: int32(offset)}, nil
}

func (s *Server) GetMaintenance(ctx context.Context, request GetMaintenanceRequestObject) (GetMaintenanceResponseObject, error) {
	record, err := s.notifications.GetMaintenance(ctx, domain.MaintenanceID(request.MaintenanceId.String()))
	if err != nil {
		if response, ok := notificationProblem(err); ok {
			return GetMaintenancedefaultApplicationProblemPlusJSONResponse(response), nil
		}
		return nil, err
	}
	mapped, err := mapMaintenance(record)
	if err != nil {
		return nil, err
	}
	return GetMaintenance200JSONResponse(mapped), nil
}

func (s *Server) EndMaintenance(ctx context.Context, request EndMaintenanceRequestObject) (EndMaintenanceResponseObject, error) {
	record, err := s.notifications.EndMaintenance(ctx, domain.MaintenanceID(request.MaintenanceId.String()))
	if err != nil {
		if response, ok := notificationProblem(err); ok {
			return EndMaintenancedefaultApplicationProblemPlusJSONResponse(response), nil
		}
		return nil, err
	}
	mapped, err := mapMaintenance(record)
	if err != nil {
		return nil, err
	}
	return EndMaintenance200JSONResponse(mapped), nil
}

func (s *Server) DeleteMaintenance(ctx context.Context, request DeleteMaintenanceRequestObject) (DeleteMaintenanceResponseObject, error) {
	if err := s.notifications.DeleteMaintenance(ctx, domain.MaintenanceID(request.MaintenanceId.String())); err != nil {
		if response, ok := notificationProblem(err); ok {
			return DeleteMaintenancedefaultApplicationProblemPlusJSONResponse(response), nil
		}
		return nil, err
	}
	return DeleteMaintenance204Response{}, nil
}

func mapMaintenance(record port.MaintenanceRecord) (Maintenance, error) {
	id, err := uuid.Parse(string(record.Interval.ID))
	if err != nil {
		return Maintenance{}, fmt.Errorf("map maintenance ID: %w", err)
	}
	monitorID, err := uuid.Parse(string(record.Interval.MonitorID))
	if err != nil {
		return Maintenance{}, fmt.Errorf("map maintenance monitor ID: %w", err)
	}
	return Maintenance{
		Id: id, MonitorId: monitorID, StartsAt: record.Interval.StartsAt,
		EndsAt: record.Interval.EndsAt, Reason: record.Interval.Reason,
		CreatedAt: record.Interval.CreatedAt, UpdatedAt: record.UpdatedAt,
	}, nil
}
