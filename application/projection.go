package application

import (
	"context"
	"time"

	"github.com/araihu/xisnove/domain"
)

func projectAggregateAndIncident(
	ctx context.Context,
	repositories Repositories,
	monitorID domain.MonitorID,
	at time.Time,
	newID func() string,
	openUnknown bool,
) error {
	_, _, err := projectAggregateAndIncidentObserved(
		ctx, repositories, monitorID, at, newID, openUnknown,
	)
	return err
}

func projectAggregateAndIncidentObserved(
	ctx context.Context,
	repositories Repositories,
	monitorID domain.MonitorID,
	at time.Time,
	newID func() string,
	openUnknown bool,
) (MonitorTransitionObservation, bool, error) {
	required, err := repositories.Health.ListRequiredLocations(ctx, monitorID)
	if err != nil {
		return MonitorTransitionObservation{}, false, err
	}
	aggregate := domain.RollupRequired(required)
	monitorHealth, err := repositories.Health.GetMonitor(ctx, monitorID)
	if err != nil {
		return MonitorTransitionObservation{}, false, err
	}
	previousAggregate := monitorHealth.State
	transitioned := aggregate != monitorHealth.State
	if aggregate != monitorHealth.State {
		monitorHealth.State = aggregate
		monitorHealth.LastTransitionAt = at.UTC()
		if err := repositories.Health.UpsertMonitor(ctx, monitorHealth); err != nil {
			return MonitorTransitionObservation{}, false, err
		}
	}

	active, err := repositories.Incidents.GetActive(ctx, monitorID)
	if err != nil {
		return MonitorTransitionObservation{}, false, err
	}
	if active == nil && aggregate == domain.HealthUnknown && !openUnknown {
		return MonitorTransitionObservation{From: previousAggregate, To: aggregate}, transitioned, nil
	}
	decision := domain.DecideIncident(
		active,
		monitorID,
		aggregate,
		at,
		func() domain.IncidentID { return domain.IncidentID(newID()) },
	)
	if decision.Action == domain.IncidentOpen {
		decision.PreviousState = previousAggregate
	}
	if decision.Action == domain.IncidentNone {
		return MonitorTransitionObservation{From: previousAggregate, To: aggregate}, transitioned, nil
	}
	err = RecordIncidentTransition(ctx, repositories, decision, at, newID)
	return MonitorTransitionObservation{From: previousAggregate, To: aggregate}, transitioned, err
}

func notificationAction(action domain.IncidentAction) domain.NotificationAction {
	switch action {
	case domain.IncidentOpen:
		return domain.NotificationOpen
	case domain.IncidentRecover:
		return domain.NotificationRecover
	default:
		return domain.NotificationChange
	}
}
