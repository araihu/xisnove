package domain

import (
	"errors"
	"strings"
	"time"
)

// ErrInvalidStateTick identifies malformed immutable state history records.
var ErrInvalidStateTick = errors.New("invalid state tick")

// MonitorLifecycle is the administrative lifecycle applied when a state tick
// is recorded. It is deliberately separate from HealthState.
type MonitorLifecycle string

const (
	MonitorLifecycleActive   MonitorLifecycle = "active"
	MonitorLifecyclePaused   MonitorLifecycle = "paused"
	MonitorLifecycleDisabled MonitorLifecycle = "disabled"
)

// StateTickActorKind identifies the principal that caused or recorded a tick.
type StateTickActorKind string

const (
	StateTickActorUser   StateTickActorKind = "user"
	StateTickActorSystem StateTickActorKind = "system"
	StateTickActorAgent  StateTickActorKind = "agent"
)

// StateTickReasonCode is a stable, machine-readable explanation for a
// lifecycle or health evaluation. Reasons are never reconstructed from text.
type StateTickReasonCode string

const (
	StateTickReasonInitial           StateTickReasonCode = "initial"
	StateTickReasonProbeSuccess      StateTickReasonCode = "probe_success"
	StateTickReasonProbeFailure      StateTickReasonCode = "probe_failure"
	StateTickReasonProbeTimeout      StateTickReasonCode = "probe_timeout"
	StateTickReasonStaleObservation  StateTickReasonCode = "stale_observation"
	StateTickReasonAgentDisconnected StateTickReasonCode = "agent_disconnected"
	StateTickReasonDependencyUnknown StateTickReasonCode = "dependency_unknown"
	StateTickReasonDependencyPaused  StateTickReasonCode = "dependency_paused"
	StateTickReasonMonitorPaused     StateTickReasonCode = "monitor_paused"
	StateTickReasonLocationPaused    StateTickReasonCode = "location_paused"
	StateTickReasonPausedByUser      StateTickReasonCode = "paused_by_user"
	StateTickReasonResumedByUser     StateTickReasonCode = "resumed_by_user"
	StateTickReasonMaintenance       StateTickReasonCode = "maintenance"
)

// StateTickActor records actor kind and optional stable actor identity.
type StateTickActor struct {
	Kind StateTickActorKind
	ID   string
}

// StateTick is an immutable lifecycle/health evaluation with causal
// provenance. Optional IDs are pointers to preserve absent versus present
// semantics at application/API boundaries.
type StateTick struct {
	ID                 string
	MonitorID          MonitorID
	LocationID         *LocationID
	Lifecycle          MonitorLifecycle
	Health             HealthState
	ReasonCode         StateTickReasonCode
	ActionID           string
	UserActionID       *string
	Actor              StateTickActor
	OccurredAt         time.Time
	ObservationID      *string
	CausalTickID       *string
	CausalDependencyID *string
}

// NewStateTickParams contains all fields needed to create one validated tick.
type NewStateTickParams struct {
	ID                 string
	MonitorID          MonitorID
	LocationID         *LocationID
	Lifecycle          MonitorLifecycle
	Health             HealthState
	ReasonCode         StateTickReasonCode
	ActionID           string
	UserActionID       *string
	Actor              StateTickActor
	OccurredAt         time.Time
	ObservationID      *string
	CausalTickID       *string
	CausalDependencyID *string
}

// NewStateTick validates and copies a state tick. Returned optional IDs do not
// alias caller-owned pointers.
func NewStateTick(params NewStateTickParams) (StateTick, error) {
	tick := StateTick{
		ID: params.ID, MonitorID: params.MonitorID,
		LocationID: cloneStateTickLocationID(params.LocationID),
		Lifecycle:  params.Lifecycle, Health: params.Health,
		ReasonCode: params.ReasonCode, ActionID: params.ActionID,
		UserActionID: cloneStateTickString(params.UserActionID),
		Actor:        params.Actor, OccurredAt: params.OccurredAt.UTC(),
		ObservationID:      cloneStateTickString(params.ObservationID),
		CausalTickID:       cloneStateTickString(params.CausalTickID),
		CausalDependencyID: cloneStateTickString(params.CausalDependencyID),
	}
	if err := tick.Validate(); err != nil {
		return StateTick{}, err
	}
	return tick, nil
}

// Validate checks the immutable StateTick shape without changing it.
func (tick StateTick) Validate() error {
	if blankStateTickID(tick.ID) || blankStateTickID(string(tick.MonitorID)) ||
		blankStateTickID(tick.ActionID) || tick.OccurredAt.IsZero() ||
		!validMonitorLifecycle(tick.Lifecycle) || !validHealthState(tick.Health) ||
		!validStateTickReason(tick.ReasonCode) || !validStateTickActor(tick.Actor) {
		return ErrInvalidStateTick
	}
	if tick.LocationID != nil && blankStateTickID(string(*tick.LocationID)) {
		return ErrInvalidStateTick
	}
	for _, id := range []*string{tick.UserActionID, tick.ObservationID, tick.CausalTickID, tick.CausalDependencyID} {
		if id != nil && blankStateTickID(*id) {
			return ErrInvalidStateTick
		}
	}
	if tick.CausalTickID != nil && *tick.CausalTickID == tick.ID {
		return ErrInvalidStateTick
	}
	return nil
}

// Clone returns a value copy with independent optional provenance pointers.
func (tick StateTick) Clone() StateTick {
	tick.LocationID = cloneStateTickLocationID(tick.LocationID)
	tick.UserActionID = cloneStateTickString(tick.UserActionID)
	tick.ObservationID = cloneStateTickString(tick.ObservationID)
	tick.CausalTickID = cloneStateTickString(tick.CausalTickID)
	tick.CausalDependencyID = cloneStateTickString(tick.CausalDependencyID)
	return tick
}

func validMonitorLifecycle(value MonitorLifecycle) bool {
	switch value {
	case MonitorLifecycleActive, MonitorLifecyclePaused, MonitorLifecycleDisabled:
		return true
	default:
		return false
	}
}

func validHealthState(value HealthState) bool {
	switch value {
	case HealthPending, HealthUp, HealthDown, HealthDegraded, HealthUnknown:
		return true
	default:
		return false
	}
}

func validStateTickReason(value StateTickReasonCode) bool {
	switch value {
	case StateTickReasonInitial,
		StateTickReasonProbeSuccess,
		StateTickReasonProbeFailure,
		StateTickReasonProbeTimeout,
		StateTickReasonStaleObservation,
		StateTickReasonAgentDisconnected,
		StateTickReasonDependencyUnknown,
		StateTickReasonDependencyPaused,
		StateTickReasonMonitorPaused,
		StateTickReasonLocationPaused,
		StateTickReasonPausedByUser,
		StateTickReasonResumedByUser,
		StateTickReasonMaintenance:
		return true
	default:
		return false
	}
}

func validStateTickActor(value StateTickActor) bool {
	if value.ID != "" && blankStateTickID(value.ID) {
		return false
	}
	switch value.Kind {
	case StateTickActorUser, StateTickActorSystem, StateTickActorAgent:
		return true
	default:
		return false
	}
}

func blankStateTickID(value string) bool {
	return strings.TrimSpace(value) == ""
}

func cloneStateTickLocationID(value *LocationID) *LocationID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneStateTickString(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
