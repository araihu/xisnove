package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

func TestGetMonitorStateHistoryMapsProvenanceAndBoundsProblem(t *testing.T) {
	now := time.Date(2026, 8, 15, 15, 0, 0, 0, time.UTC)
	monitorID := "00000000-0000-4000-8000-000000000001"
	tickID := "00000000-0000-4000-8000-000000000002"
	locationID := "00000000-0000-4000-8000-000000000003"
	actorID := "00000000-0000-4000-8000-000000000004"
	actionID := "00000000-0000-4000-8000-000000000005"
	userActionID := "00000000-0000-4000-8000-000000000006"
	observationID := "00000000-0000-4000-8000-000000000007"
	causalTickID := "00000000-0000-4000-8000-000000000008"
	causalDependencyID := "00000000-0000-4000-8000-000000000009"
	tick, err := domain.NewStateTick(domain.NewStateTickParams{
		ID: tickID, MonitorID: domain.MonitorID(monitorID),
		LocationID:         stateHistoryStringLocation(locationID),
		Lifecycle:          domain.MonitorLifecycleActive,
		Health:             domain.HealthDegraded,
		ReasonCode:         domain.StateTickReasonProbeFailure,
		ActionID:           actionID,
		UserActionID:       stateHistoryStringPointer(userActionID),
		Actor:              domain.StateTickActor{Kind: domain.StateTickActorAgent, ID: actorID},
		OccurredAt:         now.Add(-time.Minute),
		ObservationID:      stateHistoryStringPointer(observationID),
		CausalTickID:       stateHistoryStringPointer(causalTickID),
		CausalDependencyID: stateHistoryStringPointer(causalDependencyID),
	})
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewStateTickHistoryServiceWithClock(
		&stateHistoryStore{monitorID: domain.MonitorID(monitorID), ticks: []domain.StateTick{tick}},
		func() time.Time { return now },
	)
	server := NewServer(ServerConfig{StateHistory: service})

	response, err := server.GetMonitorStateHistory(context.Background(), GetMonitorStateHistoryRequestObject{
		MonitorId: mustStateHistoryUUID(t, monitorID),
	})
	if err != nil {
		t.Fatal(err)
	}
	history, ok := response.(GetMonitorStateHistory200JSONResponse)
	if !ok || history.MonitorId.String() != monitorID || len(history.Ticks) != 1 || history.Truncated {
		t.Fatalf("history response = %#v", response)
	}
	got := history.Ticks[0]
	if got.Id.String() != tickID || got.MonitorId.String() != monitorID || got.ActionId.String() != actionID ||
		got.Lifecycle != Active || got.Health != HealthStateDegraded ||
		got.ReasonCode != StateTickReasonCodeProbeFailure || got.Actor.Kind != StateTickActorKindAgent ||
		got.Actor.Id == nil || got.Actor.Id.String() != actorID || got.LocationId == nil || got.LocationId.String() != locationID ||
		got.UserActionId == nil || got.UserActionId.String() != userActionID || got.ObservationId == nil || got.ObservationId.String() != observationID ||
		got.CausalTickId == nil || got.CausalTickId.String() != causalTickID || got.CausalDependencyId == nil || got.CausalDependencyId.String() != causalDependencyID {
		t.Fatalf("mapped tick = %#v", got)
	}
	if !history.StartsAt.Equal(now.Add(-3*time.Hour)) || !history.EndsAt.Equal(now) || !history.GeneratedAt.Equal(now) {
		t.Fatalf("history bounds = %#v", history)
	}

	invalidLimit := HistoryLimit(0)
	problemResponse, err := server.GetMonitorStateHistory(context.Background(), GetMonitorStateHistoryRequestObject{
		MonitorId: mustStateHistoryUUID(t, monitorID),
		Params:    GetMonitorStateHistoryParams{Limit: &invalidLimit},
	})
	if err != nil {
		t.Fatal(err)
	}
	problem, ok := problemResponse.(GetMonitorStateHistorydefaultApplicationProblemPlusJSONResponse)
	if !ok || problem.StatusCode != 400 || problem.Body.Code != "validation_failed" {
		t.Fatalf("invalid limit response = %#v", problemResponse)
	}

	missingResponse, err := server.GetMonitorStateHistory(context.Background(), GetMonitorStateHistoryRequestObject{
		MonitorId: mustStateHistoryUUID(t, "00000000-0000-4000-8000-000000000099"),
	})
	if err != nil {
		t.Fatal(err)
	}
	notFound, ok := missingResponse.(GetMonitorStateHistorydefaultApplicationProblemPlusJSONResponse)
	if !ok || notFound.StatusCode != 404 || notFound.Body.Code != "not_found" {
		t.Fatalf("missing monitor response = %#v", missingResponse)
	}
}

func stateHistoryStringLocation(value string) *domain.LocationID {
	location := domain.LocationID(value)
	return &location
}

func stateHistoryStringPointer(value string) *string { return &value }

func mustStateHistoryUUID(t *testing.T, value string) uuid.UUID {
	t.Helper()
	parsed, err := uuid.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

type stateHistoryStore struct {
	monitorID domain.MonitorID
	ticks     []domain.StateTick
}

func (s *stateHistoryStore) View(ctx context.Context, fn func(context.Context, port.Repositories) error) error {
	return fn(ctx, port.Repositories{Monitors: stateHistoryMonitorRepository{monitorID: s.monitorID}, StateTicks: stateHistoryTickRepository{ticks: s.ticks}})
}

func (s *stateHistoryStore) Transact(ctx context.Context, fn func(context.Context, port.Repositories) error) error {
	return s.View(ctx, fn)
}

type stateHistoryMonitorRepository struct{ monitorID domain.MonitorID }

func (r stateHistoryMonitorRepository) Create(context.Context, domain.Monitor) error {
	return nil
}
func (r stateHistoryMonitorRepository) Get(_ context.Context, id domain.MonitorID) (domain.Monitor, error) {
	if id != r.monitorID {
		return domain.Monitor{}, port.ErrNotFound
	}
	return domain.Monitor{ID: id}, nil
}
func (stateHistoryMonitorRepository) AssignLocation(context.Context, port.MonitorLocation) error {
	return nil
}
func (stateHistoryMonitorRepository) GetAssignment(context.Context, domain.MonitorID) (port.MonitorLocation, error) {
	return port.MonitorLocation{}, nil
}
func (stateHistoryMonitorRepository) ListDue(context.Context, time.Time, int) ([]port.DueMonitor, error) {
	return nil, nil
}
func (stateHistoryMonitorRepository) AdvanceNextRun(context.Context, domain.MonitorID, time.Time, time.Time) (bool, error) {
	return false, nil
}

type stateHistoryTickRepository struct{ ticks []domain.StateTick }

func (r stateHistoryTickRepository) ListStateTicks(context.Context, domain.MonitorID, time.Time, time.Time, int) ([]domain.StateTick, error) {
	return r.ticks, nil
}
