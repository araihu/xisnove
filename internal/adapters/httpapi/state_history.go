package httpapi

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
)

func (s *Server) GetMonitorStateHistory(
	ctx context.Context,
	request GetMonitorStateHistoryRequestObject,
) (GetMonitorStateHistoryResponseObject, error) {
	if s.stateHistory == nil {
		return nil, fmt.Errorf("monitor state history service is not configured")
	}
	var limit *int
	if request.Params.Limit != nil {
		value := int(*request.Params.Limit)
		limit = &value
	}
	view, err := s.stateHistory.GetMonitorStateHistory(
		ctx,
		domain.MonitorID(request.MonitorId.String()),
		request.Params.StartsAt,
		request.Params.EndsAt,
		limit,
	)
	if err != nil {
		problem, status, mapped := problemFromError(err)
		if mapped {
			return GetMonitorStateHistorydefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
		}
		return nil, err
	}
	mapped, err := mapStateTickHistory(view)
	if err != nil {
		return nil, err
	}
	return GetMonitorStateHistory200JSONResponse(mapped), nil
}

func mapStateTickHistory(view application.StateTickHistoryView) (MonitorStateHistory, error) {
	monitorID, err := uuid.Parse(string(view.MonitorID))
	if err != nil {
		return MonitorStateHistory{}, fmt.Errorf("map state history monitor ID: %w", err)
	}
	ticks := make([]MonitorStateTick, len(view.Ticks))
	for index, tick := range view.Ticks {
		mapped, err := mapStateTick(tick)
		if err != nil {
			return MonitorStateHistory{}, fmt.Errorf("map state history tick %d: %w", index, err)
		}
		ticks[index] = mapped
	}
	return MonitorStateHistory{
		MonitorId: monitorID, StartsAt: view.StartsAt, EndsAt: view.EndsAt,
		GeneratedAt: view.GeneratedAt, Ticks: ticks, Truncated: view.Truncated,
	}, nil
}

func mapStateTick(tick domain.StateTick) (MonitorStateTick, error) {
	id, err := uuid.Parse(tick.ID)
	if err != nil {
		return MonitorStateTick{}, fmt.Errorf("map state tick ID: %w", err)
	}
	monitorID, err := uuid.Parse(string(tick.MonitorID))
	if err != nil {
		return MonitorStateTick{}, fmt.Errorf("map state tick monitor ID: %w", err)
	}
	actionID, err := uuid.Parse(tick.ActionID)
	if err != nil {
		return MonitorStateTick{}, fmt.Errorf("map state tick action ID: %w", err)
	}
	actor, err := mapStateTickActor(tick.Actor)
	if err != nil {
		return MonitorStateTick{}, err
	}
	mapped := MonitorStateTick{
		Id: id, MonitorId: monitorID, Lifecycle: MonitorLifecycle(tick.Lifecycle),
		Health: HealthState(tick.Health), ReasonCode: StateTickReasonCode(tick.ReasonCode),
		ActionId: actionID, Actor: actor, OccurredAt: tick.OccurredAt,
	}
	if tick.LocationID != nil {
		value, err := uuid.Parse(string(*tick.LocationID))
		if err != nil {
			return MonitorStateTick{}, fmt.Errorf("map state tick location ID: %w", err)
		}
		mapped.LocationId = &value
	}
	var errOptional error
	if mapped.UserActionId, errOptional = parseStateTickOptionalUUID(tick.UserActionID, "user action ID"); errOptional != nil {
		return MonitorStateTick{}, errOptional
	}
	if mapped.ObservationId, errOptional = parseStateTickOptionalUUID(tick.ObservationID, "observation ID"); errOptional != nil {
		return MonitorStateTick{}, errOptional
	}
	if mapped.CausalTickId, errOptional = parseStateTickOptionalUUID(tick.CausalTickID, "causal tick ID"); errOptional != nil {
		return MonitorStateTick{}, errOptional
	}
	if mapped.CausalDependencyId, errOptional = parseStateTickOptionalUUID(tick.CausalDependencyID, "causal dependency ID"); errOptional != nil {
		return MonitorStateTick{}, errOptional
	}
	return mapped, nil
}

func mapStateTickActor(actor domain.StateTickActor) (StateTickActor, error) {
	mapped := StateTickActor{Kind: StateTickActorKind(actor.Kind)}
	if actor.ID == "" {
		return mapped, nil
	}
	id, err := uuid.Parse(actor.ID)
	if err != nil {
		return StateTickActor{}, fmt.Errorf("map state tick actor ID: %w", err)
	}
	mapped.Id = &id
	return mapped, nil
}

func parseStateTickOptionalUUID(value *string, label string) (*uuid.UUID, error) {
	if value == nil {
		return nil, nil
	}
	id, err := uuid.Parse(*value)
	if err != nil {
		return nil, fmt.Errorf("map state tick %s: %w", label, err)
	}
	return &id, nil
}
