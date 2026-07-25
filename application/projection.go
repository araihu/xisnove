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
	required, err := repositories.Health.ListRequiredLocations(ctx, monitorID)
	if err != nil {
		return err
	}
	aggregate := domain.RollupRequired(required)
	monitorHealth, err := repositories.Health.GetMonitor(ctx, monitorID)
	if err != nil {
		return err
	}
	if aggregate != monitorHealth.State {
		monitorHealth.State = aggregate
		monitorHealth.LastTransitionAt = at.UTC()
		if err := repositories.Health.UpsertMonitor(ctx, monitorHealth); err != nil {
			return err
		}
	}

	active, err := repositories.Incidents.GetActive(ctx, monitorID)
	if err != nil {
		return err
	}
	if active == nil && aggregate == domain.HealthUnknown && !openUnknown {
		return nil
	}
	decision := domain.DecideIncident(
		active,
		monitorID,
		aggregate,
		at,
		func() domain.IncidentID { return domain.IncidentID(newID()) },
	)
	if decision.Action == domain.IncidentNone {
		return nil
	}
	switch decision.Action {
	case domain.IncidentOpen:
		if err := repositories.Incidents.Open(ctx, decision.Incident); err != nil {
			return err
		}
	case domain.IncidentChange, domain.IncidentRecover:
		if err := repositories.Incidents.Update(ctx, decision.Incident); err != nil {
			return err
		}
	}
	return repositories.Incidents.AppendEvent(ctx, domain.IncidentEvent{
		ID: newID(), IncidentID: decision.Incident.ID,
		Action:        notificationAction(decision.Action),
		PreviousState: decision.PreviousState,
		State:         decision.Incident.State, Severity: decision.Incident.Severity,
		CreatedAt: at.UTC(),
	})
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
