package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNewStateTickNormalizesTimeAndClonesProvenance(t *testing.T) {
	locationID := LocationID("location-1")
	userActionID := "user-action-1"
	observationID := "observation-1"
	causalTickID := "tick-parent"
	causalDependencyID := "dependency-1"
	at := time.Date(2026, 8, 15, 10, 0, 0, 0, time.FixedZone("test", -3*60*60))

	tick, err := NewStateTick(NewStateTickParams{
		ID: "tick-1", MonitorID: "monitor-1", LocationID: &locationID,
		Lifecycle: MonitorLifecycleActive, Health: HealthUnknown,
		ReasonCode: StateTickReasonDependencyUnknown, ActionID: "action-1",
		UserActionID: &userActionID, Actor: StateTickActor{
			Kind: StateTickActorSystem, ID: "system-1",
		}, OccurredAt: at, ObservationID: &observationID,
		CausalTickID: &causalTickID, CausalDependencyID: &causalDependencyID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !tick.OccurredAt.Equal(at.UTC()) || tick.OccurredAt.Location() != time.UTC {
		t.Fatalf("OccurredAt = %#v, want UTC", tick.OccurredAt)
	}

	locationID = "changed-location"
	userActionID = "changed-action"
	observationID = "changed-observation"
	causalTickID = "changed-tick"
	causalDependencyID = "changed-dependency"
	if *tick.LocationID != "location-1" || *tick.UserActionID != "user-action-1" ||
		*tick.ObservationID != "observation-1" || *tick.CausalTickID != "tick-parent" ||
		*tick.CausalDependencyID != "dependency-1" {
		t.Fatalf("constructor retained mutable provenance: %#v", tick)
	}
	if got := tick.Clone(); got.LocationID == tick.LocationID {
		t.Fatal("Clone reused LocationID pointer")
	}
}

func TestNewStateTickRejectsInvalidShape(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	valid := NewStateTickParams{
		ID: "tick-1", MonitorID: "monitor-1", Lifecycle: MonitorLifecycleActive,
		Health: HealthUp, ReasonCode: StateTickReasonProbeSuccess, ActionID: "action-1",
		Actor: StateTickActor{Kind: StateTickActorAgent, ID: "agent-1"}, OccurredAt: now,
	}
	tests := []struct {
		name string
		edit func(*NewStateTickParams)
	}{
		{name: "missing id", edit: func(p *NewStateTickParams) { p.ID = "" }},
		{name: "missing monitor", edit: func(p *NewStateTickParams) { p.MonitorID = "" }},
		{name: "missing action", edit: func(p *NewStateTickParams) { p.ActionID = "" }},
		{name: "zero occurrence", edit: func(p *NewStateTickParams) { p.OccurredAt = time.Time{} }},
		{name: "unknown lifecycle", edit: func(p *NewStateTickParams) { p.Lifecycle = "running" }},
		{name: "unknown health", edit: func(p *NewStateTickParams) { p.Health = "running" }},
		{name: "unknown reason", edit: func(p *NewStateTickParams) { p.ReasonCode = "free-form" }},
		{name: "unknown actor", edit: func(p *NewStateTickParams) { p.Actor.Kind = "controller" }},
		{name: "blank actor id", edit: func(p *NewStateTickParams) { p.Actor.ID = "   " }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := valid
			test.edit(&params)
			if _, err := NewStateTick(params); !errors.Is(err, ErrInvalidStateTick) {
				t.Fatalf("error = %v, want ErrInvalidStateTick", err)
			}
		})
	}
}

func TestStateTickRejectsSelfCausality(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	causalTickID := "tick-1"
	_, err := NewStateTick(NewStateTickParams{
		ID: "tick-1", MonitorID: "monitor-1", Lifecycle: MonitorLifecyclePaused,
		Health: HealthUnknown, ReasonCode: StateTickReasonPausedByUser,
		ActionID: "action-1", Actor: StateTickActor{Kind: StateTickActorUser, ID: "user-1"},
		OccurredAt: now, CausalTickID: &causalTickID,
	})
	if !errors.Is(err, ErrInvalidStateTick) {
		t.Fatalf("error = %v, want self-causality rejection", err)
	}
}

func TestStateTickAcceptsAdministrativeAndDependencyReasons(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	dependencyID := "dependency-1"
	userActionID := "user-action-1"
	cases := []struct {
		name      string
		lifecycle MonitorLifecycle
		health    HealthState
		reason    StateTickReasonCode
		actor     StateTickActor
	}{
		{
			name: "paused by user remains administrative unknown", lifecycle: MonitorLifecyclePaused,
			health: HealthUnknown, reason: StateTickReasonPausedByUser,
			actor: StateTickActor{Kind: StateTickActorUser, ID: "user-1"},
		},
		{
			name: "maintenance remains administrative unknown", lifecycle: MonitorLifecyclePaused,
			health: HealthUnknown, reason: StateTickReasonMaintenance,
			actor: StateTickActor{Kind: StateTickActorSystem},
		},
		{
			name: "dependency unknown preserves causal provenance", lifecycle: MonitorLifecycleActive,
			health: HealthUnknown, reason: StateTickReasonDependencyUnknown,
			actor: StateTickActor{Kind: StateTickActorSystem},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			params := NewStateTickParams{
				ID: "tick-" + test.name, MonitorID: "monitor-1", Lifecycle: test.lifecycle,
				Health: test.health, ReasonCode: test.reason, ActionID: "action-1",
				Actor: test.actor, OccurredAt: now,
			}
			if test.reason == StateTickReasonPausedByUser {
				params.UserActionID = &userActionID
			}
			if test.reason == StateTickReasonDependencyUnknown {
				params.CausalDependencyID = &dependencyID
			}
			tick, err := NewStateTick(params)
			if err != nil {
				t.Fatal(err)
			}
			if tick.Health != test.health || tick.ReasonCode != test.reason || tick.Lifecycle != test.lifecycle {
				t.Fatalf("tick = %#v, reason/lifecycle changed", tick)
			}
		})
	}
}
