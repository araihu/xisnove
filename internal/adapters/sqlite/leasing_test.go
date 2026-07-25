package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestCompetingAgentsHaveSingleLeaseWinnerAndExpiredLeaseIsReclaimed(t *testing.T) {
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(db)
	repositories := store.Repositories()
	now := time.Now().UTC().Add(-time.Minute)
	location, err := domain.NewLocation("location-1", "public", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Locations.Create(context.Background(), location); err != nil {
		t.Fatal(err)
	}
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID:                "monitor-1",
		Name:              "website",
		Interval:          time.Minute,
		Timeout:           5 * time.Second,
		FailureThreshold:  1,
		RecoveryThreshold: 1,
		HTTP: domain.HTTPProbe{
			URL:            "https://example.com",
			ExpectedStatus: []domain.StatusRange{{Min: 200, Max: 200}},
		},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Monitors.Create(context.Background(), monitor); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Monitors.AssignLocation(context.Background(), application.MonitorLocation{
		MonitorID: monitor.ID, LocationID: location.ID, Required: true,
	}); err != nil {
		t.Fatal(err)
	}
	if inserted, err := repositories.Runs.Insert(context.Background(), application.NewRunRecord{
		ID:           "run-1",
		MonitorID:    monitor.ID,
		LocationID:   location.ID,
		ScheduledFor: now,
		Probe:        monitor.Probe(),
		Timeout:      monitor.Timeout,
	}); err != nil {
		t.Fatal(err)
	} else if !inserted {
		t.Fatal("run was not inserted")
	}
	tcpMonitor, err := domain.NewTCPMonitor(domain.NewTCPMonitorParams{
		ID: "monitor-tcp", Name: "postgres", Interval: time.Minute,
		Timeout: 5 * time.Second, FailureThreshold: 1, RecoveryThreshold: 1,
		TCP:       domain.TCPProbe{Host: "postgres.internal", Port: 5432},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Monitors.Create(context.Background(), tcpMonitor); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Monitors.AssignLocation(
		context.Background(),
		application.MonitorLocation{
			MonitorID: tcpMonitor.ID, LocationID: location.ID, Required: true,
		},
	); err != nil {
		t.Fatal(err)
	}
	if inserted, err := repositories.Runs.Insert(context.Background(), application.NewRunRecord{
		ID: "tcp-run", MonitorID: tcpMonitor.ID, LocationID: location.ID,
		ScheduledFor: now.Add(-time.Minute), Probe: tcpMonitor.Probe(),
		Timeout: tcpMonitor.Timeout,
	}); err != nil {
		t.Fatal(err)
	} else if !inserted {
		t.Fatal("TCP run was not inserted")
	}
	agentIDs := []domain.AgentID{"agent-1", "agent-2"}
	for _, id := range agentIDs {
		agent, err := domain.NewAgent(domain.NewAgentParams{
			ID:                   id,
			LocationID:           location.ID,
			Name:                 string(id),
			Capabilities:         []domain.AgentCapability{domain.CapabilityHTTP},
			CredentialGeneration: 1,
			CreatedAt:            now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := repositories.Agents.Create(context.Background(), application.AgentRecord{
			Agent: agent, CredentialHash: []byte("credential-" + string(id)),
		}); err != nil {
			t.Fatal(err)
		}
	}

	service := application.NewLeaseService(application.LeaseServiceConfig{
		Store: store, Tokens: xiscrypto.NewProductionTokenIssuer(),
		LeaseDuration: 30 * time.Second,
	})
	start := make(chan struct{})
	works := make([]*application.ProbeWork, 2)
	errs := make([]error, 2)
	var group sync.WaitGroup
	for i, agentID := range agentIDs {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			works[i], errs[i] = service.LeaseProbe(
				context.Background(),
				agentID,
				[]domain.AgentCapability{domain.CapabilityHTTP},
				0,
			)
		}()
	}
	close(start)
	group.Wait()

	winner := -1
	for i := range errs {
		if errs[i] == nil {
			if winner != -1 {
				t.Fatalf("multiple winners: %#v", works)
			}
			winner = i
			continue
		}
		if !errors.Is(errs[i], application.ErrNoWork) {
			t.Fatalf("agent %d error = %v", i, errs[i])
		}
	}
	if winner == -1 {
		t.Fatalf("no lease winner: errors=%v", errs)
	}
	if works[winner].RunID != "run-1" {
		t.Fatalf("HTTP-only agent leased incompatible run %#v", works[winner])
	}

	expiredAt := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	if _, err := db.Exec(
		"UPDATE check_runs SET lease_expires_at = ? WHERE id = ?",
		expiredAt,
		"run-1",
	); err != nil {
		t.Fatal(err)
	}
	loser := 1 - winner
	reclaimed, err := service.LeaseProbe(
		context.Background(),
		agentIDs[loser],
		[]domain.AgentCapability{domain.CapabilityHTTP},
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed.RunID != works[winner].RunID ||
		reclaimed.LeaseToken == works[winner].LeaseToken {
		t.Fatalf("first=%#v reclaimed=%#v", works[winner], reclaimed)
	}
	run, err := repositories.Runs.Get(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.LeaseAttempt != 2 {
		t.Fatalf("LeaseAttempt = %d", run.LeaseAttempt)
	}
}
