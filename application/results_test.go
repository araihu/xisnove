package application_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestThirdFailureOpensOneIncidentAndDuplicateIsHarmless(t *testing.T) {
	ctx := context.Background()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "results.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}

	store := sqlitestore.NewStore(db)
	tokens := xiscrypto.NewProductionTokenIssuer()
	now := time.Now().UTC().Truncate(time.Millisecond)
	repositories := store.Repositories()
	location, err := domain.NewLocation("00000000-0000-4000-8000-000000000001", "public", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Locations.Create(ctx, location); err != nil {
		t.Fatal(err)
	}
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID:                "00000000-0000-4000-8000-000000000002",
		Name:              "service",
		Interval:          time.Minute,
		Timeout:           5 * time.Second,
		FailureThreshold:  3,
		RecoveryThreshold: 2,
		HTTP: domain.HTTPProbe{
			URL:            "https://example.com",
			ExpectedStatus: []domain.StatusRange{{Min: 200, Max: 299}},
		},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Monitors.Create(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Monitors.AssignLocation(ctx, application.MonitorLocation{
		MonitorID: monitor.ID, LocationID: location.ID, Required: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Health.UpsertLocation(ctx, domain.LocationHealth{
		MonitorID: monitor.ID, LocationID: location.ID,
		State: domain.HealthPending, LastTransitionAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Health.UpsertMonitor(ctx, domain.MonitorHealth{
		MonitorID: monitor.ID, State: domain.HealthPending, LastTransitionAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	agent, err := domain.NewAgent(domain.NewAgentParams{
		ID:                   "00000000-0000-4000-8000-000000000003",
		LocationID:           location.ID,
		Name:                 "edge",
		Capabilities:         []domain.AgentCapability{domain.CapabilityHTTP},
		CredentialGeneration: 1,
		CreatedAt:            now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Agents.Create(ctx, application.AgentRecord{
		Agent: agent, CredentialHash: []byte("agent"),
	}); err != nil {
		t.Fatal(err)
	}

	ids := 0
	var resultObservations []application.ResultObservation
	var transitionObservations []application.MonitorTransitionObservation
	service := application.NewResultService(application.ResultServiceConfig{
		Store:  store,
		Tokens: tokens,
		Now:    func() time.Time { return now },
		NewID: func() string {
			ids++
			return fmt.Sprintf("00000000-0000-4000-8000-%012d", ids+10)
		},
		LeaseDuration: 45 * time.Second,
		ObserveResult: func(observation application.ResultObservation) {
			resultObservations = append(resultObservations, observation)
		},
		ObserveMonitorTransition: func(observation application.MonitorTransitionObservation) {
			transitionObservations = append(transitionObservations, observation)
		},
	})

	var last application.ProbeResultCommand
	for i := 1; i <= 3; i++ {
		runID := domain.CheckRunID(fmt.Sprintf("00000000-0000-4000-8000-%012d", i+100))
		scheduled := now.Add(time.Duration(i) * time.Second)
		if _, err := repositories.Runs.Insert(ctx, application.NewRunRecord{
			ID: runID, MonitorID: monitor.ID, LocationID: location.ID,
			ScheduledFor: scheduled, Probe: monitor.Probe(), Timeout: monitor.Timeout,
		}); err != nil {
			t.Fatal(err)
		}
		rawLease := fmt.Sprintf("lease-token-that-is-long-enough-%d", i)
		if _, err := repositories.Runs.ClaimProbe(ctx, application.ClaimRunParams{
			AgentID: agent.ID, Capabilities: []domain.AgentCapability{domain.CapabilityHTTP},
			LeaseTokenHash: tokens.Hash(rawLease),
			LeaseExpiresAt: now.Add(time.Minute), Now: scheduled,
		}); err != nil {
			t.Fatal(err)
		}
		errorCode := "status_mismatch"
		diagnostic := "HTTP 503"
		if i == 3 {
			errorCode = "timeout"
			diagnostic = "secret probe diagnostic"
		}
		last = application.ProbeResultCommand{
			ID:    fmt.Sprintf("00000000-0000-4000-8000-%012d", i+200),
			RunID: runID, LeaseToken: rawLease,
			StartedAt: now, FinishedAt: now.Add(time.Second),
			Outcome: application.ProbeFailed, Latency: time.Second,
			ErrorCode: errorCode, DiagnosticSample: diagnostic,
		}
		acks, err := service.UploadBatch(ctx, agent.ID, []application.ProbeResultCommand{last})
		if err != nil {
			t.Fatal(err)
		}
		if acks[0].Status != application.ResultAccepted {
			t.Fatalf("ack = %#v", acks[0])
		}
	}

	health, err := repositories.Health.GetMonitor(ctx, monitor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if health.State != domain.HealthDown {
		t.Fatalf("health = %s", health.State)
	}
	history, err := repositories.Results.ListMonitorHistory(
		ctx, monitor.ID, now.Add(-time.Minute), now.Add(2*time.Minute), 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("history length = %d, want bounded length 2", len(history))
	}
	if history[0].At.After(history[1].At) {
		t.Fatalf("history is not chronological: %#v", history)
	}
	if history[0].MonitorID != monitor.ID || history[0].LocationID != location.ID {
		t.Fatalf("history identity = %#v", history[0])
	}
	if history[0].Passed || history[1].Passed {
		t.Fatalf("history outcome unexpectedly passed: %#v", history)
	}
	if history[0].Latency != time.Second || history[1].Latency != time.Second {
		t.Fatalf("history latency = %#v", history)
	}
	historyView, err := application.NewMonitorHistoryServiceWithClock(store, func() time.Time { return now.Add(2 * time.Minute) }).GetMonitorAvailabilityHistory(
		ctx, monitor.ID, ptrTime(now.Add(-time.Minute)), ptrTime(now.Add(2*time.Minute)), ptrInt(2),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !historyView.Truncated || len(historyView.Samples) != 2 || historyView.Samples[0].At.After(historyView.Samples[1].At) {
		t.Fatalf("history view = %#v", historyView)
	}
	if _, err := repositories.Results.ListMonitorHistory(
		ctx, monitor.ID, now.Add(time.Minute), now, 10,
	); err == nil {
		t.Fatal("reversed history window was accepted")
	}
	locationHealth, err := repositories.Health.GetLocation(ctx, monitor.ID, location.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantStaleAt := last.FinishedAt.Add(2*monitor.Interval + monitor.Timeout + 45*time.Second)
	if !locationHealth.StaleAt.Equal(wantStaleAt) {
		t.Fatalf("StaleAt = %v, want %v", locationHealth.StaleAt, wantStaleAt)
	}
	incident, err := store.Repositories().Incidents.GetActive(ctx, monitor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if incident == nil || incident.Severity != domain.IncidentCritical {
		t.Fatalf("incident = %#v", incident)
	}

	acks, err := service.UploadBatch(ctx, agent.ID, []application.ProbeResultCommand{last})
	if err != nil {
		t.Fatal(err)
	}
	if acks[0].Status != application.ResultDuplicate {
		t.Fatalf("duplicate ack = %#v", acks[0])
	}
	wantResults := []application.ResultObservation{
		{Status: application.ResultAccepted, Outcome: application.ProbeFailed, Latency: time.Second},
		{Status: application.ResultAccepted, Outcome: application.ProbeFailed, Latency: time.Second},
		{Status: application.ResultAccepted, Outcome: application.ProbeFailed, Latency: time.Second, TimedOut: true},
		{Status: application.ResultDuplicate},
	}
	if fmt.Sprint(resultObservations) != fmt.Sprint(wantResults) {
		t.Fatalf("result observations = %#v, want %#v", resultObservations, wantResults)
	}
	if strings.Contains(fmt.Sprint(resultObservations), "secret probe diagnostic") {
		t.Fatalf("result observation leaked diagnostic payload: %#v", resultObservations)
	}
	wantTransitions := []application.MonitorTransitionObservation{{
		From: domain.HealthPending, To: domain.HealthUnknown,
	}, {
		From: domain.HealthUnknown, To: domain.HealthDown,
	}}
	if fmt.Sprint(transitionObservations) != fmt.Sprint(wantTransitions) {
		t.Fatalf("transition observations = %#v, want %#v", transitionObservations, wantTransitions)
	}
	var eventCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM incident_events").Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("incident events = %d", eventCount)
	}
	var auditCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM audit_events").Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("audit events = %d", auditCount)
	}
}

func ptrTime(value time.Time) *time.Time { return &value }

func ptrInt(value int) *int { return &value }
