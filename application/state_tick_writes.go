package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

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

// stateTickProvenance maps an authenticated management principal to the
// immutable actor fields carried by lifecycle ticks. Legacy callers may omit
// a principal; those records intentionally retain the system actor and no
// user-action ID.
func stateTickProvenance(principal Principal, newID func() string) (domain.StateTickActor, *string) {
	if (principal.Kind == PrincipalAdmin || principal.Kind == PrincipalAPIToken) && principal.SubjectID != "" {
		return domain.StateTickActor{Kind: domain.StateTickActorUser, ID: principal.SubjectID}, stateTickUserActionID(newID)
	}
	return domain.StateTickActor{Kind: domain.StateTickActorSystem}, nil
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
	return appendAdministrativeStateTickCausal(
		ctx, repositories, monitor, locationID, lifecycle, reason, actor,
		userActionID, nil, at, newID,
	)
}

func appendAdministrativeStateTickCausal(
	ctx context.Context,
	repositories Repositories,
	monitor domain.Monitor,
	locationID *domain.LocationID,
	lifecycle domain.MonitorLifecycle,
	reason domain.StateTickReasonCode,
	actor domain.StateTickActor,
	userActionID *string,
	causalTickID *string,
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
		CausalTickID: causalTickID,
		Actor:        actor,
		OccurredAt:   at.UTC(),
	})
	if err != nil {
		return err
	}
	return appendStateTick(ctx, repositories, tick)
}

func appendMaintenanceStateTick(
	ctx context.Context,
	repositories Repositories,
	monitor domain.Monitor,
	lifecycle domain.MonitorLifecycle,
	actor domain.StateTickActor,
	userActionID *string,
	at time.Time,
	newID func() string,
) error {
	return appendMaintenanceStateTickCausal(
		ctx, repositories, monitor, lifecycle, actor, userActionID, nil, at, newID,
	)
}

func appendMaintenanceStateTickCausal(
	ctx context.Context,
	repositories Repositories,
	monitor domain.Monitor,
	lifecycle domain.MonitorLifecycle,
	actor domain.StateTickActor,
	userActionID *string,
	causalTickID *string,
	at time.Time,
	newID func() string,
) error {
	return appendAdministrativeStateTickCausal(
		ctx, repositories, monitor, nil, lifecycle,
		domain.StateTickReasonMaintenance, actor, userActionID, causalTickID, at, newID,
	)
}

func maintenanceStartStateTickIDs(maintenanceID domain.MaintenanceID) (string, string) {
	seed := []byte("xisnove:maintenance:start:" + string(maintenanceID))
	tickID := uuid.NewMD5(uuid.Nil, seed).String()
	actionID := uuid.NewMD5(uuid.Nil, append(seed, 0)).String()
	return tickID, actionID
}

// appendMaintenanceActivationStateTick is deterministic so multiple workers
// observing the same active interval converge on one immutable start tick.
// The tick is only created once the interval is active; future creation and
// cancellation therefore cannot leave a false historical transition.
func appendMaintenanceActivationStateTick(
	ctx context.Context,
	repositories Repositories,
	monitor domain.Monitor,
	maintenanceID domain.MaintenanceID,
	lifecycle domain.MonitorLifecycle,
	actor domain.StateTickActor,
	userActionID *string,
	at time.Time,
) (bool, error) {
	tickID, actionID := maintenanceStartStateTickIDs(maintenanceID)
	tick, err := domain.NewStateTick(domain.NewStateTickParams{
		ID: tickID, MonitorID: monitor.ID, Lifecycle: lifecycle,
		Health: domain.HealthUnknown, ReasonCode: domain.StateTickReasonMaintenance,
		ActionID: actionID, Actor: actor, UserActionID: userActionID,
		OccurredAt: at.UTC(),
	})
	if err != nil {
		return false, err
	}
	return appendStateTickResult(ctx, repositories, tick)
}

func maintenanceLifecycle(monitor domain.Monitor, active bool) domain.MonitorLifecycle {
	if !monitor.Enabled {
		return domain.MonitorLifecycleDisabled
	}
	if active {
		return domain.MonitorLifecyclePaused
	}
	return domain.MonitorLifecycleActive
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
	_, err := appendStateTickResult(ctx, repositories, tick)
	return err
}

func appendStateTickResult(
	ctx context.Context,
	repositories Repositories,
	tick domain.StateTick,
) (bool, error) {
	if err := tick.Validate(); err != nil {
		return false, fmt.Errorf("validate state tick: %w", err)
	}

	writer := repositories.StateTickWriter
	if writer == nil && repositories.StateTicks != nil {
		var ok bool
		writer, ok = repositories.StateTicks.(port.StateTickWriter)
		if !ok {
			return false, ErrStateTickWriterUnavailable
		}
	}
	if writer == nil {
		// Compatibility path for pre-history stores. New store composition roots
		// should always provide StateTickWriter.
		return false, nil
	}
	inserted, err := writer.AppendStateTick(ctx, tick.Clone())
	if err != nil {
		return false, fmt.Errorf("append state tick %s: %w", tick.ID, err)
	}
	return inserted, nil
}
