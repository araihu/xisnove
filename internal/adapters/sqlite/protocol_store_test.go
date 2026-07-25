package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestTCPMonitorRoundTripPreservesTypedProbe(t *testing.T) {
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "tcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(db)
	monitor, err := domain.NewTCPMonitor(domain.NewTCPMonitorParams{
		ID: "monitor-tcp", Name: "postgres", Interval: time.Minute,
		Timeout: 5 * time.Second, FailureThreshold: 3, RecoveryThreshold: 2,
		TCP: domain.TCPProbe{
			Host: "db.internal", Port: 5432,
			Send: []byte("PING\r\n"), Expect: []byte("PONG"),
			TLS: &domain.TLSExpectation{MinimumRemaining: 24 * time.Hour},
		},
		CreatedAt: time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Repositories().Monitors.Create(context.Background(), monitor); err != nil {
		t.Fatal(err)
	}
	got, err := store.Repositories().Monitors.Get(context.Background(), monitor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != domain.MonitorKindTCP ||
		got.TCP.Host != "db.internal" ||
		got.TCP.Port != 5432 ||
		string(got.TCP.Send) != "PING\r\n" ||
		string(got.TCP.Expect) != "PONG" ||
		got.TCP.TLS == nil ||
		got.TCP.TLS.MinimumRemaining != 24*time.Hour {
		t.Fatalf("monitor = %#v", got)
	}
}

func TestDNSResultRoundTripPreservesProtocolObservations(t *testing.T) {
	ctx := context.Background()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "dns-result.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(db)
	repositories := store.Repositories()
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	location, err := domain.NewLocation("location-1", "lan", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Locations.Create(ctx, location); err != nil {
		t.Fatal(err)
	}
	monitor, err := domain.NewDNSMonitor(domain.NewDNSMonitorParams{
		ID: "monitor-dns", Name: "dns", Interval: time.Minute,
		Timeout: 5 * time.Second, FailureThreshold: 3, RecoveryThreshold: 2,
		DNS: domain.DNSProbe{
			Name: "service.internal", RecordType: "A",
			ExpectedValues: []string{"10.0.0.1"},
		},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Monitors.Create(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	agent, err := domain.NewAgent(domain.NewAgentParams{
		ID: "agent-1", LocationID: location.ID, Name: "edge",
		Capabilities:         []domain.AgentCapability{domain.CapabilityDNS},
		CredentialGeneration: 1, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Agents.Create(ctx, application.AgentRecord{
		Agent: agent, CredentialHash: []byte("credential"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.Runs.Insert(ctx, application.NewRunRecord{
		ID: "run-1", MonitorID: monitor.ID, LocationID: location.ID,
		ScheduledFor: now, Probe: monitor.Probe(), Timeout: monitor.Timeout,
	}); err != nil {
		t.Fatal(err)
	}
	tlsNotAfter := now.Add(30 * 24 * time.Hour)
	inserted, err := repositories.Results.Insert(ctx, application.ProbeResultRecord{
		ID: "result-1", RunID: "run-1", AgentID: agent.ID,
		StartedAt: now, FinishedAt: now.Add(20 * time.Millisecond),
		ReceivedAt: now.Add(time.Second), Passed: true, Latency: 20 * time.Millisecond,
		ObservedValues: []string{"10.0.0.1", "10.0.0.2"},
		TLSNotAfter:    &tlsNotAfter,
		ProtocolTimings: application.ProtocolTimings{
			DNS: 4 * time.Millisecond, Connect: 6 * time.Millisecond,
		},
	})
	if err != nil || !inserted {
		t.Fatalf("inserted=%v error=%v", inserted, err)
	}
	got, err := repositories.Results.GetByID(ctx, "result-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ObservedValues) != 2 ||
		got.ObservedValues[1] != "10.0.0.2" ||
		got.TLSNotAfter == nil ||
		!got.TLSNotAfter.Equal(tlsNotAfter) ||
		got.ProtocolTimings.DNS != 4*time.Millisecond ||
		got.ProtocolTimings.Connect != 6*time.Millisecond {
		t.Fatalf("result = %#v", got)
	}
}
