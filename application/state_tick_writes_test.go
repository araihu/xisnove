package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

type recordingStateTickWriter struct {
	ticks     []domain.StateTick
	calls     int
	duplicate bool
	err       error
}

type stateTickManagementQuery struct {
	port.ManagementQueryRepository
	records []port.MonitorRecord
}

func (q stateTickManagementQuery) ListMonitors(context.Context, port.IntKeysetRequest) ([]port.MonitorRecord, error) {
	return q.records, nil
}

func (w *recordingStateTickWriter) AppendStateTick(_ context.Context, tick domain.StateTick) (bool, error) {
	w.calls++
	if w.err != nil {
		return false, w.err
	}
	if w.duplicate {
		return false, nil
	}
	w.ticks = append(w.ticks, tick.Clone())
	return true, nil
}

func stateTickWriteTestTick(t *testing.T) domain.StateTick {
	t.Helper()
	monitorID := domain.MonitorID("00000000-0000-4000-8000-000000000001")
	tick, err := domain.NewStateTick(domain.NewStateTickParams{
		ID:         "00000000-0000-4000-8000-000000000002",
		MonitorID:  monitorID,
		Lifecycle:  domain.MonitorLifecycleActive,
		Health:     domain.HealthUp,
		ReasonCode: domain.StateTickReasonProbeSuccess,
		ActionID:   "00000000-0000-4000-8000-000000000003",
		Actor:      domain.StateTickActor{Kind: domain.StateTickActorAgent, ID: "agent-1"},
		OccurredAt: time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	return tick
}

func TestAppendStateTickIsIdempotentForDuplicate(t *testing.T) {
	writer := &recordingStateTickWriter{duplicate: true}
	repositories := Repositories{StateTickWriter: writer}
	if err := appendStateTick(context.Background(), repositories, stateTickWriteTestTick(t)); err != nil {
		t.Fatalf("append duplicate: %v", err)
	}
	if writer.calls != 1 {
		t.Fatalf("writer calls = %d, want 1", writer.calls)
	}
}

func TestAppendStateTickPropagatesWriterFailure(t *testing.T) {
	want := errors.New("state tick storage unavailable")
	writer := &recordingStateTickWriter{err: want}
	repositories := Repositories{StateTickWriter: writer}
	if err := appendStateTick(context.Background(), repositories, stateTickWriteTestTick(t)); !errors.Is(err, want) {
		t.Fatalf("append error = %v, want %v", err, want)
	}
}

func TestAppendStateTickUsesLegacyNilRepositoryAsCompatibilityNoop(t *testing.T) {
	if err := appendStateTick(context.Background(), Repositories{}, stateTickWriteTestTick(t)); err != nil {
		t.Fatalf("legacy nil repository: %v", err)
	}
}

func TestAppendProbeStateTickCarriesObservationProvenance(t *testing.T) {
	writer := &recordingStateTickWriter{}
	monitorID := domain.MonitorID("00000000-0000-4000-8000-000000000011")
	locationID := domain.LocationID("00000000-0000-4000-8000-000000000012")
	finishedAt := time.Date(2026, 8, 15, 12, 1, 0, 0, time.UTC)
	command := ProbeResultCommand{
		ID: "00000000-0000-4000-8000-000000000013", Outcome: ProbeFailed,
		FinishedAt: finishedAt, ErrorCode: "status_mismatch",
	}
	err := appendProbeStateTick(
		context.Background(), Repositories{StateTickWriter: writer},
		domain.Monitor{ID: monitorID, Enabled: true},
		RunRecord{MonitorID: monitorID, LocationID: locationID}, command,
		MonitorTransitionObservation{To: domain.HealthDown},
		domain.AgentID("00000000-0000-4000-8000-000000000014"),
		monotonicIDs(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(writer.ticks) != 1 {
		t.Fatalf("ticks = %d, want 1", len(writer.ticks))
	}
	tick := writer.ticks[0]
	if tick.ReasonCode != domain.StateTickReasonProbeFailure || tick.Health != domain.HealthDown {
		t.Fatalf("tick = %#v", tick)
	}
	if tick.ObservationID == nil || *tick.ObservationID != command.ID {
		t.Fatalf("observation id = %#v", tick.ObservationID)
	}
	if tick.LocationID == nil || *tick.LocationID != locationID {
		t.Fatalf("location id = %#v", tick.LocationID)
	}
	if tick.Actor.Kind != domain.StateTickActorAgent {
		t.Fatalf("actor = %#v", tick.Actor)
	}
}

func TestAppendStaleStateTickRecordsSystemCause(t *testing.T) {
	writer := &recordingStateTickWriter{}
	monitorID := domain.MonitorID("00000000-0000-4000-8000-000000000021")
	locationID := domain.LocationID("00000000-0000-4000-8000-000000000022")
	at := time.Date(2026, 8, 15, 12, 2, 0, 0, time.UTC)
	err := appendStaleStateTick(
		context.Background(), Repositories{StateTickWriter: writer},
		domain.LocationHealth{MonitorID: monitorID, LocationID: locationID},
		domain.HealthUnknown, at, monotonicIDs(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(writer.ticks) != 1 {
		t.Fatalf("ticks = %d, want 1", len(writer.ticks))
	}
	tick := writer.ticks[0]
	if tick.ReasonCode != domain.StateTickReasonStaleObservation || tick.Actor.Kind != domain.StateTickActorSystem {
		t.Fatalf("tick = %#v", tick)
	}
	if tick.ObservationID != nil {
		t.Fatalf("stale observation id = %#v, want nil", tick.ObservationID)
	}
}

func TestAppendAdministrativeStateTickCarriesUserAction(t *testing.T) {
	writer := &recordingStateTickWriter{}
	monitorID := domain.MonitorID("00000000-0000-4000-8000-000000000031")
	locationID := domain.LocationID("00000000-0000-4000-8000-000000000032")
	userActionID := "00000000-0000-4000-8000-000000000033"
	err := appendAdministrativeStateTick(
		context.Background(), Repositories{StateTickWriter: writer},
		domain.Monitor{ID: monitorID}, &locationID,
		domain.MonitorLifecycleDisabled, domain.StateTickReasonPausedByUser,
		domain.StateTickActor{Kind: domain.StateTickActorUser, ID: "admin-1"},
		&userActionID, time.Date(2026, 8, 15, 12, 3, 0, 0, time.UTC), monotonicIDs(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(writer.ticks) != 1 {
		t.Fatalf("ticks = %d, want 1", len(writer.ticks))
	}
	tick := writer.ticks[0]
	if tick.Lifecycle != domain.MonitorLifecycleDisabled || tick.ReasonCode != domain.StateTickReasonPausedByUser {
		t.Fatalf("tick = %#v", tick)
	}
	if tick.UserActionID == nil || *tick.UserActionID != userActionID {
		t.Fatalf("user action id = %#v", tick.UserActionID)
	}
	if tick.Actor.Kind != domain.StateTickActorUser || tick.Actor.ID != "admin-1" {
		t.Fatalf("actor = %#v", tick.Actor)
	}
	if tick.LocationID == nil || *tick.LocationID != locationID {
		t.Fatalf("location id = %#v", tick.LocationID)
	}
}

func TestAppendLocationAdministrativeStateTicksFansOutToAssignedMonitors(t *testing.T) {
	writer := &recordingStateTickWriter{}
	locationID := domain.LocationID("00000000-0000-4000-8000-000000000041")
	monitorID := domain.MonitorID("00000000-0000-4000-8000-000000000042")
	query := stateTickManagementQuery{records: []port.MonitorRecord{
		{Monitor: domain.Monitor{ID: monitorID, Enabled: true}, LocationID: locationID},
		{Monitor: domain.Monitor{ID: "00000000-0000-4000-8000-000000000043"}, LocationID: "other"},
	}}
	userActionID := "00000000-0000-4000-8000-000000000044"
	err := appendLocationAdministrativeStateTicks(
		context.Background(), Repositories{StateTickWriter: writer, Management: query}, locationID,
		domain.MonitorLifecyclePaused, domain.StateTickReasonLocationPaused,
		domain.StateTickActor{Kind: domain.StateTickActorUser, ID: "admin-1"}, &userActionID,
		time.Date(2026, 8, 15, 12, 4, 0, 0, time.UTC), monotonicIDs(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(writer.ticks) != 1 || writer.ticks[0].MonitorID != monitorID {
		t.Fatalf("ticks = %#v", writer.ticks)
	}
	if writer.ticks[0].ReasonCode != domain.StateTickReasonLocationPaused {
		t.Fatalf("reason = %s", writer.ticks[0].ReasonCode)
	}
	if writer.ticks[0].Lifecycle != domain.MonitorLifecyclePaused {
		t.Fatalf("lifecycle = %s", writer.ticks[0].Lifecycle)
	}
}

var _ port.StateTickWriter = (*recordingStateTickWriter)(nil)
