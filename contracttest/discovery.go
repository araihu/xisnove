package contracttest

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

func RunDiscovery(t *testing.T, factory Factory) {
	t.Helper()
	RunOperatorEdge(t, factory)
	t.Run("complete snapshots atomically mark absence and preserve freshness", func(t *testing.T) { testCompleteDiscoverySnapshots(t, factory(t)) })
	t.Run("discovery idempotency stale and promotion", func(t *testing.T) { testDiscoveryLifecycle(t, factory(t)) })
	t.Run("discovery transaction rollback", func(t *testing.T) { testDiscoveryRollback(t, factory(t)) })
	t.Run("unknown tombstone is a no-op", func(t *testing.T) { testUnknownDiscoveryTombstone(t, factory(t)) })
	t.Run("newer observation wins concurrently", func(t *testing.T) { testConcurrentDiscoveryOrdering(t, factory(t)) })
	t.Run("promotion is concurrent and idempotent", func(t *testing.T) { testConcurrentDiscoveryPromotion(t, factory(t)) })
	t.Run("cursor traverses every page and terminates", func(t *testing.T) { testDiscoveryCursorTraversal(t, factory(t)) })
}

func testCompleteDiscoverySnapshots(t *testing.T, unit port.UnitOfWork) {
	t.Helper()
	discovery := unit.(port.DiscoveryUnitOfWork)
	ctx := context.Background()
	initial := time.Date(2026, 7, 26, 13, 0, 0, 0, time.UTC)
	completed := initial.Add(10 * time.Minute)
	agentID, locationID := seedDiscoveryOwner(t, ctx, unit, initial)
	present := mustDiscoveryCandidate(t, "00000000-0000-4000-8000-000000000941", agentID, locationID, true, initial)
	missing := mustDiscoveryCandidate(t, "00000000-0000-4000-8000-000000000942", agentID, locationID, true, initial)
	missing.SourceUID = "uid-2"
	applyDiscoveryBatch(t, ctx, discovery, port.DiscoveryBatch{ID: "complete-base", AgentID: agentID, RequestHash: "complete-base", Candidates: []domain.DiscoveryCandidate{present, missing}, CreatedAt: initial})

	monitor := mustDiscoveryMonitor(t, "00000000-0000-4000-8000-000000000943", initial)
	if err := discovery.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		if err := repositories.Monitors.Create(ctx, monitor); err != nil {
			return err
		}
		_, err := repositories.Discovery.LinkPromotion(ctx, present.ID, monitor.ID, initial)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	present.LastObservedAt, present.UpdatedAt = completed, completed
	completeBatch := port.DiscoveryBatch{ID: "complete-current", AgentID: agentID, RequestHash: "complete-current", Candidates: []domain.DiscoveryCandidate{present}, Complete: true, CompletedAt: completed, CreatedAt: completed.Add(time.Minute)}
	applyDiscoveryBatch(t, ctx, discovery, completeBatch)

	if err := discovery.DiscoveryView(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		storedPresent, err := repositories.Discovery.Get(ctx, present.ID)
		if err != nil {
			return err
		}
		storedMissing, err := repositories.Discovery.Get(ctx, missing.ID)
		if err != nil {
			return err
		}
		if !storedPresent.Present || !storedPresent.LastObservedAt.Equal(completed) || storedPresent.PromotedMonitorID == nil || *storedPresent.PromotedMonitorID != monitor.ID {
			t.Fatalf("complete present candidate = %#v", storedPresent)
		}
		if storedMissing.Present || storedMissing.PromotedMonitorID != nil {
			t.Fatalf("complete missing candidate = %#v", storedMissing)
		}
		completeReader, ok := repositories.Discovery.(port.CompleteDiscoveryRepository)
		if !ok {
			t.Fatal("discovery repository does not expose complete observation freshness")
		}
		latest, err := completeReader.LastCompleteAt(ctx, agentID)
		if err != nil || latest == nil || !latest.Equal(completed) {
			t.Fatalf("last complete = %v, %v", latest, err)
		}
		agent, err := repositories.Agents.Get(ctx, agentID)
		if err != nil || agent.LastCompleteDiscoveryAt == nil || !agent.LastCompleteDiscoveryAt.Equal(completed) {
			t.Fatalf("agent complete discovery = %#v, %v", agent.LastCompleteDiscoveryAt, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := discovery.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		_, err := repositories.Discovery.ApplyBatch(ctx, port.DiscoveryBatch{ID: "empty-partial", AgentID: agentID, RequestHash: "empty-partial", CreatedAt: completed})
		return err
	}); !errors.Is(err, port.ErrConflict) {
		t.Fatalf("empty partial = %v, want conflict", err)
	}
	if err := discovery.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		_, err := repositories.Discovery.ApplyBatch(ctx, port.DiscoveryBatch{ID: "empty-complete", AgentID: agentID, RequestHash: "empty-complete", Complete: true, CompletedAt: completed.Add(time.Minute), CreatedAt: completed.Add(2 * time.Minute)})
		return err
	}); err != nil {
		t.Fatalf("empty complete: %v", err)
	}
	if err := discovery.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		_, err := repositories.Discovery.ApplyBatch(ctx, port.DiscoveryBatch{ID: "complete-current", AgentID: agentID, RequestHash: "complete-current", Candidates: []domain.DiscoveryCandidate{present}, Complete: false, CreatedAt: completed})
		return err
	}); !errors.Is(err, port.ErrConflict) {
		t.Fatalf("changed completeness replay = %v, want conflict", err)
	}
	changedTime := completeBatch
	changedTime.CompletedAt = completed.Add(time.Second)
	if err := discovery.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		_, err := repositories.Discovery.ApplyBatch(ctx, changedTime)
		return err
	}); !errors.Is(err, port.ErrConflict) {
		t.Fatalf("changed timestamp replay = %v, want conflict", err)
	}
}

func testUnknownDiscoveryTombstone(t *testing.T, unit port.UnitOfWork) {
	t.Helper()
	discovery := unit.(port.DiscoveryUnitOfWork)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	agentID, locationID := seedDiscoveryOwner(t, ctx, unit, now)
	tombstone := mustDiscoveryCandidate(t, "00000000-0000-4000-8000-000000000920", agentID, locationID, false, now)
	for index, batchID := range []string{"unknown-tombstone-1", "unknown-tombstone-2"} {
		var ack port.DiscoveryBatchAcknowledgement
		err := discovery.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
			var err error
			ack, err = repositories.Discovery.ApplyBatch(ctx, port.DiscoveryBatch{ID: batchID, AgentID: agentID, RequestHash: batchID, Candidates: []domain.DiscoveryCandidate{tombstone}, CreatedAt: now.Add(time.Duration(index) * time.Second)})
			return err
		})
		if err != nil || ack != (port.DiscoveryBatchAcknowledgement{Accepted: 1}) {
			t.Fatalf("batch %d = %#v, %v", index, ack, err)
		}
	}
	err := discovery.DiscoveryView(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		_, err := repositories.Discovery.Get(ctx, tombstone.ID)
		return err
	})
	if !errors.Is(err, port.ErrNotFound) {
		t.Fatalf("unknown tombstone persisted: %v", err)
	}
}

func testConcurrentDiscoveryOrdering(t *testing.T, unit port.UnitOfWork) {
	t.Helper()
	discovery := unit.(port.DiscoveryUnitOfWork)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 9, 10, 0, 0, time.UTC)
	agentID, locationID := seedDiscoveryOwner(t, ctx, unit, now)
	base := mustDiscoveryCandidate(t, "00000000-0000-4000-8000-000000000921", agentID, locationID, true, now)
	applyDiscoveryBatch(t, ctx, discovery, port.DiscoveryBatch{ID: "ordering-base", AgentID: agentID, RequestHash: "ordering-base", Candidates: []domain.DiscoveryCandidate{base}, CreatedAt: now})
	older := base.Clone()
	older.Name = "older"
	older.LastObservedAt = now.Add(100 * time.Microsecond)
	older.UpdatedAt = older.LastObservedAt
	newer := base.Clone()
	newer.Name = "newer"
	newer.LastObservedAt = now.Add(200 * time.Microsecond)
	newer.UpdatedAt = newer.LastObservedAt

	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, batch := range []port.DiscoveryBatch{
		{ID: "ordering-older", AgentID: agentID, RequestHash: "ordering-older", Candidates: []domain.DiscoveryCandidate{older}, CreatedAt: older.LastObservedAt},
		{ID: "ordering-newer", AgentID: agentID, RequestHash: "ordering-newer", Candidates: []domain.DiscoveryCandidate{newer}, CreatedAt: newer.LastObservedAt},
	} {
		batch := batch
		go func() {
			<-start
			errs <- discovery.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
				_, err := repositories.Discovery.ApplyBatch(ctx, batch)
				return err
			})
		}()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if err := discovery.DiscoveryView(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		stored, err := repositories.Discovery.Get(ctx, base.ID)
		if err == nil && (stored.Name != "newer" || !stored.LastObservedAt.Equal(newer.LastObservedAt)) {
			t.Errorf("stored observation = %#v", stored)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func testConcurrentDiscoveryPromotion(t *testing.T, unit port.UnitOfWork) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 9, 20, 0, 0, time.UTC)
	agentID, locationID := seedDiscoveryOwner(t, ctx, unit, now)
	monitorIDs := []string{"00000000-0000-4000-8000-000000000923", "00000000-0000-4000-8000-000000000924"}
	var generated atomic.Uint32
	service := application.NewDiscoveryService(application.DiscoveryServiceConfig{
		Store:          unit.(port.DiscoveryUnitOfWork),
		NewCandidateID: func() string { return "00000000-0000-4000-8000-000000000922" },
		NewMonitorID:   func() string { return monitorIDs[int(generated.Add(1))-1] },
		Now:            func() time.Time { return now },
	})
	ack, err := service.Publish(ctx, agentID, "promotion-base", []application.DiscoveryInput{{
		LocationID: locationID, SourceKind: "service", SourceUID: "uid-1", Namespace: "default", Name: "api",
		Labels: map[string]string{"app": "api"}, Protocol: domain.MonitorKindHTTP,
		Target: "https://api.default.svc/health", NetworkPerspective: "cluster-a", Present: true, ObservedAt: now,
	}})
	if err != nil || ack != (port.DiscoveryBatchAcknowledgement{Accepted: 1, Created: 1}) {
		t.Fatalf("publish = %#v, %v", ack, err)
	}
	command := application.DiscoveryPromotionCommand{
		Name: "promoted", LocationID: locationID, RequiredLocation: true,
		Interval: time.Minute, Timeout: 5 * time.Second, FailureThreshold: 2, RecoveryThreshold: 1,
	}

	start := make(chan struct{})
	results := make(chan application.DiscoveryPromotion, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			result, err := service.Promote(ctx, "00000000-0000-4000-8000-000000000922", command)
			results <- result
			errs <- err
		}()
	}
	close(start)
	var got []application.DiscoveryPromotion
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		got = append(got, <-results)
	}
	if got[0].Monitor.ID == "" || got[0].Monitor.ID != got[1].Monitor.ID || generated.Load() != 1 {
		t.Fatalf("promotion results = %#v, generated monitor ids=%d", got, generated.Load())
	}
	if err := unit.View(ctx, func(ctx context.Context, repositories port.Repositories) error {
		_, err := repositories.Monitors.Get(ctx, got[0].Monitor.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func testDiscoveryCursorTraversal(t *testing.T, unit port.UnitOfWork) {
	t.Helper()
	discovery := unit.(port.DiscoveryUnitOfWork)
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 9, 30, 0, 0, time.UTC)
	agentID, locationID := seedDiscoveryOwner(t, ctx, unit, now)
	ids := []domain.DiscoveryCandidateID{
		"00000000-0000-4000-8000-000000000931", "00000000-0000-4000-8000-000000000932",
		"00000000-0000-4000-8000-000000000933", "00000000-0000-4000-8000-000000000934",
		"00000000-0000-4000-8000-000000000935",
	}
	candidates := make([]domain.DiscoveryCandidate, 0, len(ids))
	for _, id := range ids {
		candidate := mustDiscoveryCandidate(t, string(id), agentID, locationID, true, now)
		candidate.SourceUID = string(id)
		candidates = append(candidates, candidate)
	}
	applyDiscoveryBatch(t, ctx, discovery, port.DiscoveryBatch{ID: "cursor", AgentID: agentID, RequestHash: "cursor", Candidates: candidates, CreatedAt: now})

	var got []domain.DiscoveryCandidateID
	var after domain.DiscoveryCandidateID
	for {
		var page []domain.DiscoveryCandidate
		if err := discovery.DiscoveryView(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
			var err error
			page, err = repositories.Discovery.List(ctx, port.DiscoveryListRequest{Limit: 2, After: after})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		if len(page) > 2 {
			t.Fatalf("page too large: %d", len(page))
		}
		for _, candidate := range page {
			got = append(got, candidate.ID)
		}
		after = page[len(page)-1].ID
	}
	if !slices.Equal(got, ids) {
		t.Fatalf("cursor traversal = %v, want %v", got, ids)
	}
}

func applyDiscoveryBatch(t *testing.T, ctx context.Context, discovery port.DiscoveryUnitOfWork, batch port.DiscoveryBatch) {
	t.Helper()
	if err := discovery.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		_, err := repositories.Discovery.ApplyBatch(ctx, batch)
		return err
	}); err != nil {
		t.Fatal(err)
	}
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
		present := false
		listed, err := repositories.Discovery.List(ctx, port.DiscoveryListRequest{Filter: port.DiscoveryFilter{Present: &present}, Limit: 10})
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
