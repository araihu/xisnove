package contracttest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

func RunDiscovery(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("discovery idempotency stale and promotion", func(t *testing.T) { testDiscoveryLifecycle(t, factory(t)) })
	t.Run("discovery transaction rollback", func(t *testing.T) { testDiscoveryRollback(t, factory(t)) })
}

func testDiscoveryLifecycle(t *testing.T, unit port.UnitOfWork) {
	t.Helper()
	discovery, ok := unit.(port.DiscoveryUnitOfWork)
	if !ok {
		t.Fatal("store does not implement DiscoveryUnitOfWork")
	}
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 10, 0, 0, 123, time.UTC)
	agentID, locationID := seedDiscoveryOwner(t, ctx, unit, now)
	candidate := mustDiscoveryCandidate(t, "00000000-0000-4000-8000-000000000903", agentID, locationID, true, now)
	batch := port.DiscoveryBatch{ID: "batch-1", AgentID: agentID, RequestHash: "hash-1", Candidates: []domain.DiscoveryCandidate{candidate}, CreatedAt: now}
	var first port.DiscoveryBatchAcknowledgement
	if err := discovery.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		var err error
		first, err = repositories.Discovery.ApplyBatch(ctx, batch)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if first != (port.DiscoveryBatchAcknowledgement{Accepted: 1, Created: 1}) {
		t.Fatalf("first=%#v", first)
	}
	var replay port.DiscoveryBatchAcknowledgement
	if err := discovery.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		var err error
		replay, err = repositories.Discovery.ApplyBatch(ctx, batch)
		return err
	}); err != nil || replay != first {
		t.Fatalf("replay=%#v error=%v", replay, err)
	}
	batch.RequestHash = "changed"
	err := discovery.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		_, err := repositories.Discovery.ApplyBatch(ctx, batch)
		return err
	})
	if !errors.Is(err, port.ErrConflict) {
		t.Fatalf("changed replay error=%v", err)
	}

	monitor := mustDiscoveryMonitor(t, "00000000-0000-4000-8000-000000000904", now)
	if err := unit.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		if err := repositories.Monitors.Create(ctx, monitor); err != nil {
			return err
		}
		return repositories.Monitors.AssignLocation(ctx, port.MonitorLocation{MonitorID: monitor.ID, LocationID: locationID, Required: true})
	}); err != nil {
		t.Fatal(err)
	}
	if err := discovery.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		linked, err := repositories.Discovery.LinkPromotion(ctx, candidate.ID, monitor.ID, now.Add(time.Second))
		if err == nil && !linked {
			t.Error("first promotion link was not won")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := discovery.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		linked, err := repositories.Discovery.LinkPromotion(ctx, candidate.ID, monitor.ID, now.Add(time.Second))
		if err == nil && linked {
			t.Error("second promotion link unexpectedly won")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}

	stale := candidate
	stale.Present = false
	stale.LastObservedAt = now.Add(2 * time.Second)
	stale.UpdatedAt = stale.LastObservedAt
	staleBatch := port.DiscoveryBatch{ID: "batch-2", AgentID: agentID, RequestHash: "hash-2", Candidates: []domain.DiscoveryCandidate{stale}, CreatedAt: now.Add(2 * time.Second)}
	if err := discovery.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		_, err := repositories.Discovery.ApplyBatch(ctx, staleBatch)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := discovery.DiscoveryView(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		stored, err := repositories.Discovery.Get(ctx, candidate.ID)
		if err != nil {
			return err
		}
		if stored.Present || stored.PromotedMonitorID == nil || *stored.PromotedMonitorID != monitor.ID {
			t.Fatalf("stored=%#v", stored)
		}
		listed, err := repositories.Discovery.List(ctx, port.DiscoveryListRequest{Filter: port.DiscoveryFilter{State: port.DiscoveryStateStale}, Limit: 10})
		if err != nil {
			return err
		}
		if len(listed) != 1 || listed[0].ID != candidate.ID {
			t.Fatalf("stale list=%#v", listed)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func testDiscoveryRollback(t *testing.T, unit port.UnitOfWork) {
	t.Helper()
	discovery := unit.(port.DiscoveryUnitOfWork)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 11, 0, 0, 0, time.UTC)
	agentID, locationID := seedDiscoveryOwner(t, ctx, unit, now)
	candidate := mustDiscoveryCandidate(t, "00000000-0000-4000-8000-000000000913", agentID, locationID, true, now)
	stop := errors.New("stop")
	err := discovery.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		if _, err := repositories.Discovery.ApplyBatch(ctx, port.DiscoveryBatch{ID: "rollback", AgentID: agentID, RequestHash: "rollback", Candidates: []domain.DiscoveryCandidate{candidate}, CreatedAt: now}); err != nil {
			return err
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("rollback error=%v", err)
	}
	err = discovery.DiscoveryView(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		_, err := repositories.Discovery.Get(ctx, candidate.ID)
		return err
	})
	if !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("rolled back candidate error=%v", err)
	}
	committedBatch := port.DiscoveryBatch{ID: "promotion-rollback", AgentID: agentID, RequestHash: "promotion-rollback", Candidates: []domain.DiscoveryCandidate{candidate}, CreatedAt: now}
	if err := discovery.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		_, err := repositories.Discovery.ApplyBatch(ctx, committedBatch)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	monitor := mustDiscoveryMonitor(t, "00000000-0000-4000-8000-000000000914", now)
	err = discovery.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		if err := repositories.Monitors.Create(ctx, monitor); err != nil {
			return err
		}
		if err := repositories.Monitors.AssignLocation(ctx, port.MonitorLocation{MonitorID: monitor.ID, LocationID: locationID, Required: true}); err != nil {
			return err
		}
		if linked, err := repositories.Discovery.LinkPromotion(ctx, candidate.ID, monitor.ID, now); err != nil || !linked {
			return err
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("promotion rollback error=%v", err)
	}
	if err := unit.View(ctx, func(ctx context.Context, repositories port.Repositories) error {
		_, err := repositories.Monitors.Get(ctx, monitor.ID)
		return err
	}); !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("rolled back monitor error=%v", err)
	}
	if err := discovery.DiscoveryView(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		stored, err := repositories.Discovery.Get(ctx, candidate.ID)
		if err == nil && stored.PromotedMonitorID != nil {
			t.Errorf("rolled back promotion=%#v", stored)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func seedDiscoveryOwner(t *testing.T, ctx context.Context, unit port.UnitOfWork, now time.Time) (domain.AgentID, domain.LocationID) {
	t.Helper()
	locationID := domain.LocationID("00000000-0000-4000-8000-000000000901")
	agentID := domain.AgentID("00000000-0000-4000-8000-000000000902")
	location, err := domain.NewLocation(locationID, "cluster", now)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := domain.NewAgent(domain.NewAgentParams{ID: agentID, LocationID: locationID, Name: "agent", Capabilities: []domain.AgentCapability{domain.CapabilityHTTP, domain.CapabilityKubernetesDiscovery}, CredentialGeneration: 1, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := unit.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		if err := repositories.Locations.Create(ctx, location); err != nil {
			return err
		}
		return repositories.Agents.Create(ctx, port.AgentRecord{Agent: agent, CredentialHash: []byte("discovery-credential")})
	}); err != nil {
		t.Fatal(err)
	}
	return agentID, locationID
}

func mustDiscoveryCandidate(t *testing.T, id string, agentID domain.AgentID, locationID domain.LocationID, present bool, now time.Time) domain.DiscoveryCandidate {
	t.Helper()
	candidate, err := domain.NewDiscoveryCandidate(domain.NewDiscoveryCandidateParams{ID: domain.DiscoveryCandidateID(id), AgentID: agentID, LocationID: locationID, SourceKind: "service", SourceUID: "uid-1", Namespace: "default", Name: "api", Labels: map[string]string{"app": "api"}, Protocol: domain.MonitorKindHTTP, Target: "https://api.default.svc/health", NetworkPerspective: "cluster-a", Present: present, ObservedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

func mustDiscoveryMonitor(t *testing.T, id string, now time.Time) domain.Monitor {
	t.Helper()
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{ID: domain.MonitorID(id), Name: "promoted", Labels: map[string]string{}, Interval: time.Minute, Timeout: 5 * time.Second, FailureThreshold: 2, RecoveryThreshold: 1, HTTP: domain.HTTPProbe{Method: "GET", URL: "https://api.default.svc/health", Headers: map[string]string{}, ExpectedStatus: []domain.StatusRange{{Min: 200, Max: 399}}}, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	return monitor
}
