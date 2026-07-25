package domain

import "time"

type HealthState string

const (
	HealthPending  HealthState = "pending"
	HealthUp       HealthState = "up"
	HealthDown     HealthState = "down"
	HealthDegraded HealthState = "degraded"
	HealthUnknown  HealthState = "unknown"
)

type Thresholds struct {
	Failures   uint16
	Recoveries uint16
}

type ProbeObservation struct {
	Passed bool
	At     time.Time
}

type LocationHealth struct {
	MonitorID            MonitorID
	LocationID           LocationID
	State                HealthState
	ConsecutiveFailures  uint16
	ConsecutiveSuccesses uint16
	LastObservedAt       time.Time
	LastTransitionAt     time.Time
	StaleAt              time.Time
}

type MonitorHealth struct {
	MonitorID        MonitorID
	State            HealthState
	LastTransitionAt time.Time
}

func ApplyProbe(
	health LocationHealth,
	observation ProbeObservation,
	thresholds Thresholds,
) LocationHealth {
	previous := health.State
	if observation.Passed {
		health.ConsecutiveSuccesses++
		health.ConsecutiveFailures = 0
		if health.ConsecutiveSuccesses >= thresholds.Recoveries {
			health.State = HealthUp
		}
	} else {
		health.ConsecutiveFailures++
		health.ConsecutiveSuccesses = 0
		if health.ConsecutiveFailures >= thresholds.Failures {
			health.State = HealthDown
		}
	}

	health.LastObservedAt = observation.At.UTC()
	if health.State != previous {
		health.LastTransitionAt = observation.At.UTC()
	}
	return health
}

func RollupRequired(locations []LocationHealth) HealthState {
	if len(locations) == 0 {
		return HealthUnknown
	}

	up := 0
	down := 0
	for _, location := range locations {
		switch location.State {
		case HealthUnknown, HealthPending:
			return HealthUnknown
		case HealthUp:
			up++
		case HealthDown:
			down++
		case HealthDegraded:
			return HealthDegraded
		default:
			return HealthUnknown
		}
	}

	if up == len(locations) {
		return HealthUp
	}
	if down == len(locations) {
		return HealthDown
	}
	return HealthDegraded
}
