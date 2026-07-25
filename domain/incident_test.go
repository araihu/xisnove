package domain_test

import (
	"testing"
	"time"

	"github.com/araihu/xisnove/domain"
)

func TestDecideIncidentOpensCriticalIncidentForDownMonitor(t *testing.T) {
	at := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	decision := domain.DecideIncident(
		nil,
		"monitor-1",
		domain.HealthDown,
		at,
		func() domain.IncidentID { return "incident-1" },
	)

	if decision.Action != domain.IncidentOpen {
		t.Fatalf("Action = %q", decision.Action)
	}
	if decision.Incident.ID != "incident-1" {
		t.Fatalf("Incident.ID = %q", decision.Incident.ID)
	}
	if decision.Incident.State != domain.HealthDown {
		t.Fatalf("Incident.State = %q", decision.Incident.State)
	}
	if decision.Incident.Severity != domain.IncidentCritical {
		t.Fatalf("Incident.Severity = %q", decision.Incident.Severity)
	}
	if !decision.Incident.OpenedAt.Equal(at) || !decision.Incident.LastTransitionAt.Equal(at) {
		t.Fatalf("incident timestamps = %#v", decision.Incident)
	}
}

func TestDecideIncidentDoesNothingForHealthyMonitorWithoutIncident(t *testing.T) {
	decision := domain.DecideIncident(
		nil,
		"monitor-1",
		domain.HealthUp,
		time.Now(),
		func() domain.IncidentID { return "should-not-open" },
	)
	if decision.Action != domain.IncidentNone {
		t.Fatalf("Action = %q", decision.Action)
	}
	if decision.Incident.ID != "" {
		t.Fatalf("Incident.ID = %q", decision.Incident.ID)
	}
}

func TestDecideIncidentChangesSeverityWhenHealthChanges(t *testing.T) {
	openedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	at := openedAt.Add(time.Minute)
	active := &domain.Incident{
		ID:               "incident-1",
		MonitorID:        "monitor-1",
		State:            domain.HealthDown,
		Severity:         domain.IncidentCritical,
		OpenedAt:         openedAt,
		LastTransitionAt: openedAt,
	}

	decision := domain.DecideIncident(
		active,
		"monitor-1",
		domain.HealthDegraded,
		at,
		func() domain.IncidentID { return "replacement" },
	)

	if decision.Action != domain.IncidentChange {
		t.Fatalf("Action = %q", decision.Action)
	}
	if decision.PreviousState != domain.HealthDown {
		t.Fatalf("PreviousState = %q", decision.PreviousState)
	}
	if decision.Incident.ID != active.ID || decision.Incident.State != domain.HealthDegraded {
		t.Fatalf("Incident = %#v", decision.Incident)
	}
	if decision.Incident.Severity != domain.IncidentWarning {
		t.Fatalf("Incident.Severity = %q", decision.Incident.Severity)
	}
	if !decision.Incident.LastTransitionAt.Equal(at) {
		t.Fatalf("LastTransitionAt = %v", decision.Incident.LastTransitionAt)
	}
}

func TestDecideIncidentRecoversOnlyOnUp(t *testing.T) {
	openedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	at := openedAt.Add(time.Minute)
	active := &domain.Incident{
		ID:               "incident-1",
		MonitorID:        "monitor-1",
		State:            domain.HealthUnknown,
		Severity:         domain.IncidentWarning,
		OpenedAt:         openedAt,
		LastTransitionAt: openedAt,
	}

	decision := domain.DecideIncident(
		active,
		"monitor-1",
		domain.HealthUp,
		at,
		func() domain.IncidentID { return "replacement" },
	)

	if decision.Action != domain.IncidentRecover {
		t.Fatalf("Action = %q", decision.Action)
	}
	if decision.PreviousState != domain.HealthUnknown {
		t.Fatalf("PreviousState = %q", decision.PreviousState)
	}
	if decision.Incident.RecoveredAt == nil || !decision.Incident.RecoveredAt.Equal(at) {
		t.Fatalf("RecoveredAt = %v", decision.Incident.RecoveredAt)
	}
	if decision.Incident.State != domain.HealthUp {
		t.Fatalf("Incident.State = %q", decision.Incident.State)
	}
}

func TestDecideIncidentPreservesUnchangedActiveIncident(t *testing.T) {
	at := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	active := &domain.Incident{
		ID:               "incident-1",
		MonitorID:        "monitor-1",
		State:            domain.HealthDown,
		Severity:         domain.IncidentCritical,
		OpenedAt:         at,
		LastTransitionAt: at,
	}

	decision := domain.DecideIncident(
		active,
		"monitor-1",
		domain.HealthDown,
		at.Add(time.Minute),
		func() domain.IncidentID { return "replacement" },
	)

	if decision.Action != domain.IncidentNone {
		t.Fatalf("Action = %q", decision.Action)
	}
	if decision.Incident != *active {
		t.Fatalf("Incident = %#v", decision.Incident)
	}
}
