package application

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/araihu/xisnove/domain"
)

const notificationChannelPageSize = 100

type incidentTransitionAuditPayload struct {
	EventID           string                    `json:"eventId"`
	Action            domain.NotificationAction `json:"action"`
	PreviousState     domain.HealthState        `json:"previousState"`
	State             domain.HealthState        `json:"state"`
	Severity          domain.IncidentSeverity   `json:"severity"`
	NotificationCount int                       `json:"notificationCount"`
	Suppressed        bool                      `json:"suppressed"`
}

// RecordIncidentTransition persists an Incident mutation, its immutable event,
// matching delivery outbox rows, and its audit decision. Callers must invoke it
// with repositories supplied by the same UnitOfWork transaction that updates
// health, so the complete projection commits or rolls back atomically.
func RecordIncidentTransition(
	ctx context.Context,
	repositories Repositories,
	decision domain.IncidentDecision,
	at time.Time,
	newID func() string,
) error {
	if newID == nil || at.IsZero() {
		return fmt.Errorf("record incident transition: identifier generator and timestamp are required")
	}
	switch decision.Action {
	case domain.IncidentOpen, domain.IncidentChange, domain.IncidentRecover:
	default:
		return fmt.Errorf("record incident transition: unsupported action %q", decision.Action)
	}
	at = at.UTC()
	action := notificationAction(decision.Action)
	monitor, err := repositories.Monitors.Get(ctx, decision.Incident.MonitorID)
	if err != nil {
		return fmt.Errorf("load monitor for incident transition: %w", err)
	}
	event := domain.IncidentEvent{
		ID: newID(), IncidentID: decision.Incident.ID,
		Action: action, PreviousState: decision.PreviousState,
		State: decision.Incident.State, Severity: decision.Incident.Severity,
		CreatedAt: at,
	}
	switch decision.Action {
	case domain.IncidentOpen:
		if err := repositories.Incidents.Open(ctx, decision.Incident); err != nil {
			return fmt.Errorf("open incident: %w", err)
		}
	case domain.IncidentChange, domain.IncidentRecover:
		if err := repositories.Incidents.Update(ctx, decision.Incident); err != nil {
			return fmt.Errorf("update incident: %w", err)
		}
	}
	return recordIncidentEvent(ctx, repositories, decision.Incident, monitor, event, at, newID)
}

func recordIncidentEvent(
	ctx context.Context,
	repositories Repositories,
	incident domain.Incident,
	monitor domain.Monitor,
	event domain.IncidentEvent,
	at time.Time,
	newID func() string,
) error {
	routes, err := repositories.NotificationRoutes.ListEnabled(ctx)
	if err != nil {
		return fmt.Errorf("list notification routes: %w", err)
	}
	channels, err := listNotificationChannels(ctx, repositories.NotificationChannels)
	if err != nil {
		return err
	}
	byID := make(map[domain.NotificationChannelID]domain.NotificationChannel, len(channels))
	for _, record := range channels {
		byID[record.Channel.ID] = record.Channel
	}
	selected := domain.SelectNotificationRoutes(routes, byID, domain.NotificationEvent{
		Action: event.Action, Event: event, MonitorID: monitor.ID,
		Labels: monitor.MetadataLabels(),
	})
	maintenance, err := repositories.Maintenance.ListActive(ctx, monitor.ID, at)
	if err != nil {
		return fmt.Errorf("list active maintenance: %w", err)
	}
	suppressed := len(maintenance) != 0
	if err := repositories.Incidents.AppendEvent(ctx, event); err != nil {
		return fmt.Errorf("append incident event: %w", err)
	}
	for _, route := range selected {
		identity, err := domain.NewNotificationIdentity(event.ID, route.ID, route.ChannelID)
		if err != nil {
			return fmt.Errorf("build notification identity: %w", err)
		}
		state := domain.DeliveryPending
		var suppressedAt *time.Time
		if suppressed {
			state = domain.DeliverySuppressed
			value := at
			suppressedAt = &value
		}
		inserted, err := repositories.NotificationOutbox.Insert(ctx, NotificationOutboxRecord{
			ID: domain.NotificationDeliveryID(newID()), IncidentEventID: event.ID,
			RouteID: route.ID, ChannelID: route.ChannelID, DedupeKey: identity,
			RenderSnapshot: domain.RenderSnapshot{
				EventID: event.ID, Action: event.Action, IncidentID: incident.ID,
				MonitorID: monitor.ID, MonitorName: monitor.Name,
				MonitorDescription: monitor.Description, MonitorLabels: monitor.MetadataLabels(),
				PreviousState: event.PreviousState, State: event.State,
				Severity: event.Severity, OccurredAt: at,
				RouteID: route.ID, ChannelID: route.ChannelID,
				ChannelKind: byID[route.ChannelID].Kind,
				Template:    route.Template, RouteUpdatedAt: route.UpdatedAt,
			},
			State: state, AvailableAt: at, SuppressedAt: suppressedAt,
			CreatedAt: at, UpdatedAt: at,
		})
		if err != nil {
			return fmt.Errorf("insert notification outbox: %w", err)
		}
		if !inserted {
			continue
		}
	}
	payload, err := json.Marshal(incidentTransitionAuditPayload{
		EventID: event.ID, Action: event.Action, PreviousState: event.PreviousState,
		State: event.State, Severity: event.Severity,
		NotificationCount: len(selected), Suppressed: suppressed,
	})
	if err != nil {
		return fmt.Errorf("encode incident transition audit: %w", err)
	}
	incidentID := incident.ID
	if err := repositories.Audit.Append(ctx, AuditEventRecord{
		ID: newID(), Kind: "incident." + string(event.Action), SubjectKind: "monitor",
		SubjectID: string(monitor.ID), IncidentID: &incidentID,
		Payload: payload, CreatedAt: at,
	}); err != nil {
		return fmt.Errorf("append incident transition audit: %w", err)
	}
	return nil
}

func listNotificationChannels(
	ctx context.Context,
	repository NotificationChannelRepository,
) ([]NotificationChannelRecord, error) {
	var result []NotificationChannelRecord
	for offset := 0; ; offset += notificationChannelPageSize {
		page, err := repository.List(ctx, notificationChannelPageSize, offset)
		if err != nil {
			return nil, fmt.Errorf("list notification channels: %w", err)
		}
		result = append(result, page...)
		if len(page) < notificationChannelPageSize {
			return result, nil
		}
	}
}
