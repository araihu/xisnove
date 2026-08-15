package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

// ErrStateTickWriterUnavailable means a store exposed the history reader but
// has not yet wired the append side of the immutable state contract.
var ErrStateTickWriterUnavailable = errors.New("state tick writer unavailable")

func stateTickPersistenceConfigured(repositories Repositories) bool {
	return repositories.StateTickWriter != nil || repositories.StateTicks != nil
}

func stateTickIDs(newID func() string) (string, string, error) {
	if newID == nil {
		return "", "", errors.New("state tick id generator is nil")
	}
	return newID(), newID(), nil
}

func stateTickUserActionID(newID func() string) *string {
	if newID == nil {
		return nil
	}
	id := newID()
	return &id
}

func appendProbeStateTick(
	ctx context.Context,
	repositories Repositories,
	monitor domain.Monitor,
	run RunRecord,
	command ProbeResultCommand,
	transition MonitorTransitionObservation,
	agentID domain.AgentID,
	newID func() string,
) error {
	tickID, actionID, err := stateTickIDs(newID)
	if err != nil {
		return err
	}
	reason := domain.StateTickReasonProbeFailure
	if command.Outcome == ProbePassed {
		reason = domain.StateTickReasonProbeSuccess
	}
	if command.ErrorCode == "timeout" {
		reason = domain.StateTickReasonProbeTimeout
	}
	lifecycle := domain.MonitorLifecycleActive
	if !monitor.Enabled {
		lifecycle = domain.MonitorLifecycleDisabled
	}
	locationID := run.LocationID
	observationID := command.ID
	tick, err := domain.NewStateTick(domain.NewStateTickParams{
		ID:            tickID,
		MonitorID:     run.MonitorID,
		LocationID:    &locationID,
		Lifecycle:     lifecycle,
		Health:        transition.To,
		ReasonCode:    reason,
		ActionID:      actionID,
		Actor:         domain.StateTickActor{Kind: domain.StateTickActorAgent, ID: string(agentID)},
		OccurredAt:    command.FinishedAt.UTC(),
		ObservationID: &observationID,
	})
	if err != nil {
		return err
	}
	return appendStateTick(ctx, repositories, tick)
}

func appendStaleStateTick(
	ctx context.Context,
	repositories Repositories,
	candidate domain.LocationHealth,
	health domain.HealthState,
	at time.Time,
	newID func() string,
) error {
	tickID, actionID, err := stateTickIDs(newID)
	if err != nil {
		return err
	}
	locationID := candidate.LocationID
	tick, err := domain.NewStateTick(domain.NewStateTickParams{
		ID:         tickID,
		MonitorID:  candidate.MonitorID,
		LocationID: &locationID,
		Lifecycle:  domain.MonitorLifecycleActive,
		Health:     health,
		ReasonCode: domain.StateTickReasonStaleObservation,
		ActionID:   actionID,
		Actor:      domain.StateTickActor{Kind: domain.StateTickActorSystem},
		OccurredAt: at.UTC(),
	})
	if err != nil {
		return err
	}
	return appendStateTick(ctx, repositories, tick)
}

func appendAdministrativeStateTick(
	ctx context.Context,
	repositories Repositories,
	monitor domain.Monitor,
	locationID *domain.LocationID,
	lifecycle domain.MonitorLifecycle,
	reason domain.StateTickReasonCode,
	actor domain.StateTickActor,
	userActionID *string,
	at time.Time,
	newID func() string,
) error {
	tickID, actionID, err := stateTickIDs(newID)
	if err != nil {
		return err
	}
	tick, err := domain.NewStateTick(domain.NewStateTickParams{
		ID:           tickID,
		MonitorID:    monitor.ID,
		LocationID:   locationID,
		Lifecycle:    lifecycle,
		Health:       domain.HealthUnknown,
		ReasonCode:   reason,
		ActionID:     actionID,
		UserActionID: userActionID,
		Actor:        actor,
		OccurredAt:   at.UTC(),
	})
	if err != nil {
		return err
	}
	return appendStateTick(ctx, repositories, tick)
}

// appendLocationAdministrativeStateTicks fans a location lifecycle action
// out to the monitors assigned to that location. StateTick is monitor-scoped,
// so a location mutation is represented by one immutable tick per affected
// monitor. The read is intentionally skipped for legacy stores that have not
// enabled history persistence yet.
func appendLocationAdministrativeStateTicks(
	ctx context.Context,
	repositories Repositories,
	locationID domain.LocationID,
	lifecycle domain.MonitorLifecycle,
	reason domain.StateTickReasonCode,
	actor domain.StateTickActor,
	userActionID *string,
	at time.Time,
	newID func() string,
) error {
	if !stateTickPersistenceConfigured(repositories) {
		return nil
	}
	monitors, err := repositories.Management.ListMonitors(ctx, port.IntKeysetRequest{Limit: 10000})
	if err != nil {
		return fmt.Errorf("list monitors for location state tick: %w", err)
	}
	for _, record := range monitors {
		if record.LocationID != locationID {
			continue
		}
		effectiveLifecycle := lifecycle
		if reason == domain.StateTickReasonLocationPaused {
			if !record.Monitor.Enabled {
				effectiveLifecycle = domain.MonitorLifecycleDisabled
			} else {
				effectiveLifecycle = domain.MonitorLifecyclePaused
			}
		}
		if reason == domain.StateTickReasonResumedByUser && !record.Monitor.Enabled {
			effectiveLifecycle = domain.MonitorLifecycleDisabled
		}
		assignedLocationID := record.LocationID
		if err := appendAdministrativeStateTick(
			ctx, repositories, record.Monitor, &assignedLocationID, effectiveLifecycle, reason,
			actor, userActionID, at, newID,
		); err != nil {
			return err
		}
	}
	return nil
}

// appendStateTick is the single application seam for immutable lifecycle and
// health observations. It is intentionally called inside the caller's unit of
// work, so a failed append rolls back the probe/staleness/administrative
// mutation that produced the observation.
//
// StateTickWriter is optional for legacy stores which predate historical
// state. Once a store exposes StateTicks, however, it must also expose a
// writer (directly or through the same repository) so new observations cannot
// be silently dropped.
func appendStateTick(
	ctx context.Context,
	repositories Repositories,
	tick domain.StateTick,
) error {
	if err := tick.Validate(); err != nil {
		return fmt.Errorf("validate state tick: %w", err)
	}

	writer := repositories.StateTickWriter
	if writer == nil && repositories.StateTicks != nil {
		var ok bool
		writer, ok = repositories.StateTicks.(port.StateTickWriter)
		if !ok {
			return ErrStateTickWriterUnavailable
		}
	}
	if writer == nil {
		// Compatibility path for pre-history stores. New store composition roots
		// should always provide StateTickWriter.
		return nil
	}
	if _, err := writer.AppendStateTick(ctx, tick.Clone()); err != nil {
		return fmt.Errorf("append state tick %s: %w", tick.ID, err)
	}
	return nil
}
