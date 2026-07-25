package application_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
	"github.com/araihu/xisnove/internal/application"
	"github.com/araihu/xisnove/internal/domain"
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
	service := application.NewResultService(application.ResultServiceConfig{
		Store:  store,
		Tokens: tokens,
		Now:    func() time.Time { return now },
		NewID: func() string {
			ids++
			return fmt.Sprintf("00000000-0000-4000-8000-%012d", ids+10)
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
		last = application.ProbeResultCommand{
			ID:    fmt.Sprintf("00000000-0000-4000-8000-%012d", i+200),
			RunID: runID, LeaseToken: rawLease,
			StartedAt: now, FinishedAt: now.Add(time.Second),
			Outcome: application.ProbeFailed, Latency: time.Second,
			ErrorCode: "status_mismatch", DiagnosticSample: "HTTP 503",
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
	var eventCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM incident_events").Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("incident events = %d", eventCount)
	}
}
