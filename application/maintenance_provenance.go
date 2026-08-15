package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

// ErrAuditSubjectReaderUnavailable means a store configured for immutable
// history cannot recover the provenance of a delayed maintenance transition.
// Failing closed prevents a worker from silently replacing an authenticated
// user action with a system actor after a restart.
var ErrAuditSubjectReaderUnavailable = errors.New("audit subject reader unavailable")

type maintenanceAuditPayload struct {
	MonitorID    domain.MonitorID          `json:"monitorId"`
	StartsAt     time.Time                 `json:"startsAt"`
	EndsAt       *time.Time                `json:"endsAt,omitempty"`
	Reason       string                    `json:"reason,omitempty"`
	ActorKind    domain.StateTickActorKind `json:"actorKind"`
	ActorID      string                    `json:"actorId,omitempty"`
	UserActionID *string                   `json:"userActionId,omitempty"`
}

func auditSubjectReader(repositories port.Repositories) port.AuditSubjectReader {
	if repositories.AuditSubjectReader != nil {
		return repositories.AuditSubjectReader
	}
	if reader, ok := repositories.Audit.(port.AuditSubjectReader); ok {
		return reader
	}
	return nil
}

// maintenanceActivationProvenance resolves the actor captured at creation
// time. Future maintenance is intentionally not represented by a StateTick
// until activation, so this lookup is the durable bridge across restarts.
func maintenanceActivationProvenance(
	ctx context.Context,
	repositories port.Repositories,
	maintenanceID domain.MaintenanceID,
) (domain.StateTickActor, *string, error) {
	system := domain.StateTickActor{Kind: domain.StateTickActorSystem}
	if !stateTickPersistenceConfigured(repositories) {
		return system, nil, nil
	}
	reader := auditSubjectReader(repositories)
	if reader == nil {
		return system, nil, ErrAuditSubjectReaderUnavailable
	}
	events, err := reader.ListBySubject(ctx, "maintenance", string(maintenanceID))
	if err != nil {
		return system, nil, fmt.Errorf("read maintenance provenance: %w", err)
	}
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Kind != "maintenance.created" {
			continue
		}
		var payload maintenanceAuditPayload
		if err := json.Unmarshal(events[index].Payload, &payload); err != nil {
			return system, nil, fmt.Errorf("decode maintenance provenance: %w", err)
		}
		actor, err := maintenanceAuditActor(payload.ActorKind, payload.ActorID)
		if err != nil {
			return system, nil, err
		}
		if payload.UserActionID != nil && strings.TrimSpace(*payload.UserActionID) == "" {
			return system, nil, errors.New("maintenance provenance has empty user action id")
		}
		return actor, cloneOptionalString(payload.UserActionID), nil
	}
	// Older stores may contain a maintenance row without a creation audit
	// payload. Preserve compatibility while retaining a valid system actor.
	return system, nil, nil
}

func maintenanceAuditActor(kind domain.StateTickActorKind, id string) (domain.StateTickActor, error) {
	if kind == "" {
		kind = domain.StateTickActorSystem
	}
	switch kind {
	case domain.StateTickActorSystem, domain.StateTickActorUser, domain.StateTickActorAgent:
		return domain.StateTickActor{Kind: kind, ID: id}, nil
	default:
		return domain.StateTickActor{}, fmt.Errorf("maintenance provenance has invalid actor kind %q", kind)
	}
}

func cloneOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
