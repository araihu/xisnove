package domain

import "time"

type IncidentAction string

const (
	IncidentNone    IncidentAction = "none"
	IncidentOpen    IncidentAction = "open"
	IncidentChange  IncidentAction = "change"
	IncidentRecover IncidentAction = "recover"
)

type IncidentDecision struct {
	Action        IncidentAction
	Incident      Incident
	PreviousState HealthState
}

type IncidentSeverity string

const (
	IncidentWarning  IncidentSeverity = "warning"
	IncidentCritical IncidentSeverity = "critical"
)

type Incident struct {
	ID               IncidentID
	MonitorID        MonitorID
	State            HealthState
	Severity         IncidentSeverity
	OpenedAt         time.Time
	LastTransitionAt time.Time
	RecoveredAt      *time.Time
}

type IncidentEvent struct {
	ID            string
	IncidentID    IncidentID
	Action        NotificationAction
	PreviousState HealthState
	State         HealthState
	Severity      IncidentSeverity
	CreatedAt     time.Time
}

func DecideIncident(
	active *Incident,
	monitorID MonitorID,
	health HealthState,
	at time.Time,
	newID func() IncidentID,
) IncidentDecision {
	at = at.UTC()

	severity, unhealthy := severityFor(health)
	if active == nil {
		if !unhealthy {
			return IncidentDecision{Action: IncidentNone}
		}
		return IncidentDecision{
			Action: IncidentOpen,
			Incident: Incident{
				ID:               newID(),
				MonitorID:        monitorID,
				State:            health,
				Severity:         severity,
				OpenedAt:         at,
				LastTransitionAt: at,
			},
		}
	}

	incident := *active
	if health == HealthUp {
		incident.State = HealthUp
		incident.LastTransitionAt = at
		incident.RecoveredAt = &at
		return IncidentDecision{
			Action:        IncidentRecover,
			Incident:      incident,
			PreviousState: active.State,
		}
	}
	if !unhealthy || (active.State == health && active.Severity == severity) {
		return IncidentDecision{Action: IncidentNone, Incident: incident}
	}

	incident.State = health
	incident.Severity = severity
	incident.LastTransitionAt = at
	return IncidentDecision{
		Action:        IncidentChange,
		Incident:      incident,
		PreviousState: active.State,
	}
}

func severityFor(health HealthState) (IncidentSeverity, bool) {
	switch health {
	case HealthDown:
		return IncidentCritical, true
	case HealthDegraded, HealthUnknown:
		return IncidentWarning, true
	default:
		return "", false
	}
}
