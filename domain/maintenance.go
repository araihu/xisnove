package domain

import (
	"errors"
	"strings"
	"time"
)

var ErrInvalidMaintenance = errors.New("invalid maintenance interval")

const maxMaintenanceReasonBytes = 2 << 10

type MaintenanceInterval struct {
	ID                    MaintenanceID
	MonitorID             MonitorID
	StartsAt              time.Time
	EndsAt                *time.Time
	Reason                string
	EndedNotificationSent bool
	CreatedAt             time.Time
}

func NewMaintenanceInterval(
	id MaintenanceID,
	monitorID MonitorID,
	startsAt time.Time,
	endsAt *time.Time,
	reason string,
) (MaintenanceInterval, error) {
	reason = strings.TrimSpace(reason)
	if id == "" || monitorID == "" || startsAt.IsZero() || len(reason) > maxMaintenanceReasonBytes {
		return MaintenanceInterval{}, ErrInvalidMaintenance
	}
	startsAt = startsAt.UTC()
	if endsAt != nil {
		end := endsAt.UTC()
		if !end.After(startsAt) {
			return MaintenanceInterval{}, ErrInvalidMaintenance
		}
		endsAt = &end
	}
	return MaintenanceInterval{
		ID: id, MonitorID: monitorID, StartsAt: startsAt, EndsAt: endsAt,
		Reason: reason,
	}, nil
}

func (m MaintenanceInterval) ActiveAt(at time.Time) bool {
	at = at.UTC()
	return !at.Before(m.StartsAt) && (m.EndsAt == nil || at.Before(*m.EndsAt))
}

func ShouldNotifyAfterMaintenance(state HealthState, alreadyEmitted bool) bool {
	if alreadyEmitted {
		return false
	}
	switch state {
	case HealthDown, HealthDegraded, HealthUnknown:
		return true
	default:
		return false
	}
}
