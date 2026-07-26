package application_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

func TestUpsertDiscoveryBatchIsBoundedAndIdempotent(t *testing.T) {
	fixture := newDiscoveryFixture()
	inputs := []application.DiscoveryInput{fixture.input("uid-1", true)}
	first, err := fixture.service.Publish(context.Background(), "agent-1", "batch-1", inputs)
	if err != nil || first != (port.DiscoveryBatchAcknowledgement{Accepted: 1, Created: 1}) {
		t.Fatalf("first = %#v, %v", first, err)
	}
	replay, err := fixture.service.Publish(context.Background(), "agent-1", "batch-1", inputs)
	if err != nil || replay != first || len(fixture.repository.candidates) != 1 {
		t.Fatalf("replay = %#v, %v candidates=%d", replay, err, len(fixture.repository.candidates))
	}
	changed := []application.DiscoveryInput{fixture.input("uid-2", true)}
	if _, err := fixture.service.Publish(context.Background(), "agent-1", "batch-1", changed); !errors.Is(err, port.ErrConflict) {
		t.Fatalf("changed replay error = %v", err)
	}
	oversized := make([]application.DiscoveryInput, 501)
	for index := range oversized {
		oversized[index] = fixture.input(fmt.Sprintf("uid-%d", index), true)
	}
	if _, err := fixture.service.Publish(context.Background(), "agent-1", "batch-2", oversized); !errors.Is(err, application.ErrDiscoveryBatchTooLarge) {
		t.Fatalf("oversized error = %v", err)
	}
	duplicate := fixture.input("uid-duplicate", true)
	duplicateWithDifferentMetadata := duplicate
	duplicateWithDifferentMetadata.Name = "payload-order-must-not-win"
	if _, err := fixture.service.Publish(context.Background(), "agent-1", "batch-duplicates", []application.DiscoveryInput{
		duplicate, duplicateWithDifferentMetadata,
	}); err == nil {
		t.Fatal("duplicate stable discovery identity was accepted")
	} else {
		var validation *application.ValidationError
		if !errors.As(err, &validation) || validation.Fields["candidates[1]"] == "" {
			t.Fatalf("duplicate identity error = %#v", err)
		}
	}
}

func TestTombstoneMarksCandidateStaleWithoutDeletingPromotion(t *testing.T) {
	fixture := newDiscoveryFixture()
	input := fixture.input("uid-1", true)
	if _, err := fixture.service.Publish(context.Background(), "agent-1", "batch-1", []application.DiscoveryInput{input}); err != nil {
		t.Fatal(err)
	}
	candidate := fixture.repository.onlyCandidate()
	monitorID := domain.MonitorID("monitor-existing")
	candidate.PromotedMonitorID = &monitorID
	fixture.repository.candidates[candidate.ID] = candidate
	input.Present = false
	if _, err := fixture.service.Publish(context.Background(), "agent-1", "batch-2", []application.DiscoveryInput{input}); err != nil {
		t.Fatal(err)
	}
	stale := fixture.repository.onlyCandidate()
	if stale.Present || stale.PromotedMonitorID == nil || *stale.PromotedMonitorID != monitorID {
		t.Fatalf("stale candidate = %#v", stale)
	}
}

func TestUnknownTombstoneIsAnIdempotentNoOp(t *testing.T) {
	fixture := newDiscoveryFixture()
	input := fixture.input("uid-missing", false)
	first, err := fixture.service.Publish(context.Background(), "agent-1", "batch-missing-1", []application.DiscoveryInput{input})
	if err != nil || first != (port.DiscoveryBatchAcknowledgement{Accepted: 1}) {
		t.Fatalf("first = %#v, %v", first, err)
	}
	second, err := fixture.service.Publish(context.Background(), "agent-1", "batch-missing-2", []application.DiscoveryInput{input})
	if err != nil || second != first || len(fixture.repository.candidates) != 0 {
		t.Fatalf("second = %#v, %v candidates=%d", second, err, len(fixture.repository.candidates))
	}
}

func TestPromoteCandidateCreatesMonitorAndLinksProvenanceAtomically(t *testing.T) {
	fixture := newDiscoveryFixture()
	if _, err := fixture.service.Publish(context.Background(), "agent-1", "batch-1", []application.DiscoveryInput{fixture.input("uid-1", true)}); err != nil {
		t.Fatal(err)
	}
	candidate := fixture.repository.onlyCandidate()
	promotion, err := fixture.service.Promote(context.Background(), candidate.ID, application.DiscoveryPromotionCommand{
		Name: "api", LocationID: "location-1", RequiredLocation: true,
		Interval: time.Minute, Timeout: 5 * time.Second, FailureThreshold: 2, RecoveryThreshold: 1,
		Public: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if promotion.Monitor.ID != "monitor-1" || promotion.Candidate.PromotedMonitorID == nil ||
		*promotion.Candidate.PromotedMonitorID != "monitor-1" || fixture.store.transactions != 2 {
		t.Fatalf("promotion = %#v transactions=%d", promotion, fixture.store.transactions)
	}
	if _, ok := fixture.monitors.monitors["monitor-1"]; !ok {
		t.Fatal("monitor was not created")
	}
	if fixture.health.location.State != domain.HealthPending || fixture.health.monitor.State != domain.HealthPending {
		t.Fatalf("health = %#v / %#v", fixture.health.location, fixture.health.monitor)
	}
}

func TestPromoteCandidateReplaysExistingMonitor(t *testing.T) {
	fixture := newDiscoveryFixture()
	if _, err := fixture.service.Publish(context.Background(), "agent-1", "batch-1", []application.DiscoveryInput{fixture.input("uid-1", true)}); err != nil {
		t.Fatal(err)
	}
	candidate := fixture.repository.onlyCandidate()
	command := application.DiscoveryPromotionCommand{Name: "api", LocationID: "location-1", RequiredLocation: true,
		Interval: time.Minute, Timeout: 5 * time.Second, FailureThreshold: 2, RecoveryThreshold: 1}
	first, err := fixture.service.Promote(context.Background(), candidate.ID, command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.Promote(context.Background(), candidate.ID, command)
	if err != nil || second.Monitor.ID != first.Monitor.ID || len(fixture.monitors.monitors) != 1 {
		t.Fatalf("replay = %#v, %v monitors=%d", second, err, len(fixture.monitors.monitors))
	}
}

func TestPromotedCandidateReportsDriftWhenSourceChanges(t *testing.T) {
	fixture := newDiscoveryFixture()
	input := fixture.input("uid-1", true)
	if _, err := fixture.service.Publish(context.Background(), "agent-1", "batch-1", []application.DiscoveryInput{input}); err != nil {
		t.Fatal(err)
	}
	candidate := fixture.repository.onlyCandidate()
	monitorID := domain.MonitorID("monitor-existing")
	candidate.PromotedMonitorID = &monitorID
	fixture.repository.candidates[candidate.ID] = candidate
	input.Name = "renamed-api"
	if _, err := fixture.service.Publish(context.Background(), "agent-1", "batch-2", []application.DiscoveryInput{input}); err != nil {
		t.Fatal(err)
	}
	if fixture.repository.onlyCandidate().DriftHint == "" {
		t.Fatal("promoted source change did not set drift hint")
	}
}

func TestListDiscoveryCandidatesBindsPresenceAndCursorAudience(t *testing.T) {
	fixture := newDiscoveryFixture()
	first := fixture.input("uid-1", true)
	second := fixture.input("uid-2", false)
	if _, err := fixture.service.Publish(context.Background(), "agent-1", "batch-1", []application.DiscoveryInput{first}); err != nil {
		t.Fatal(err)
	}
	// An absent observation only becomes catalog state after the source was seen.
	second.Present = true
	if _, err := fixture.service.Publish(context.Background(), "agent-1", "batch-2", []application.DiscoveryInput{second}); err != nil {
		t.Fatal(err)
	}
	second.Present = false
	second.ObservedAt = second.ObservedAt.Add(time.Minute)
	if _, err := fixture.service.Publish(context.Background(), "agent-1", "batch-3", []application.DiscoveryInput{second}); err != nil {
		t.Fatal(err)
	}

	present := true
	page, err := fixture.service.List(context.Background(), port.DiscoveryFilter{State: port.DiscoveryStatePending, Present: &present}, application.PageRequest{Limit: 1})
	if err != nil || len(page.Items) != 1 || page.NextCursor != "" || !page.Items[0].Present {
		t.Fatalf("present page = %#v, %v", page, err)
	}

	all, err := fixture.service.List(context.Background(), port.DiscoveryFilter{State: port.DiscoveryStatePending}, application.PageRequest{Limit: 1})
	if err != nil || len(all.Items) != 1 || all.NextCursor == "" {
		t.Fatalf("first page = %#v, %v", all, err)
	}
	absent := false
	_, err = fixture.service.List(context.Background(), port.DiscoveryFilter{State: port.DiscoveryStatePending, Present: &absent}, application.PageRequest{Limit: 1, Cursor: all.NextCursor})
	var validation *application.ValidationError
	if !errors.As(err, &validation) || validation.Fields["cursor"] != "is invalid" {
		t.Fatalf("filter-mismatched cursor error = %#v", err)
	}
	secondPage, err := fixture.service.List(context.Background(), port.DiscoveryFilter{State: port.DiscoveryStatePending}, application.PageRequest{Limit: 1, Cursor: all.NextCursor})
	if err != nil || len(secondPage.Items) != 1 || secondPage.Items[0].ID == all.Items[0].ID || secondPage.NextCursor != "" {
		t.Fatalf("second page = %#v, %v", secondPage, err)
	}
}

type discoveryFixture struct {
	service    *application.DiscoveryService
	store      *discoveryStore
	repository *discoveryRepository
	monitors   *discoveryMonitors
	health     *discoveryHealth
}

func newDiscoveryFixture() discoveryFixture {
	repository := &discoveryRepository{candidates: map[domain.DiscoveryCandidateID]domain.DiscoveryCandidate{}, batches: map[string]storedDiscoveryBatch{}}
	monitors := &discoveryMonitors{monitors: map[domain.MonitorID]domain.Monitor{}}
	health := &discoveryHealth{}
	store := &discoveryStore{repositories: port.DiscoveryRepositories{
		Discovery: repository, Locations: discoveryLocations{}, Monitors: monitors, Health: health,
	}}
	codec, err := application.NewHMACCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		panic(err)
	}
	return discoveryFixture{service: application.NewDiscoveryService(application.DiscoveryServiceConfig{
		Store:          store,
		NewCandidateID: func() string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", len(repository.candidates)+1) },
		NewMonitorID:   sequenceIDs("monitor-1", "monitor-2"),
		Now:            func() time.Time { return time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC) },
		Cursors:        codec,
	}), store: store, repository: repository, monitors: monitors, health: health}
}

func (f discoveryFixture) input(uid string, present bool) application.DiscoveryInput {
	return application.DiscoveryInput{LocationID: "location-1", SourceKind: "service", SourceUID: uid,
		Namespace: "default", Name: "api", Labels: map[string]string{"app": "api"},
		Protocol: domain.MonitorKindHTTP, Target: "https://api.default.svc/health",
		NetworkPerspective: "cluster-a", Present: present, ObservedAt: time.Date(2026, 7, 26, 11, 0, 0, 0, time.UTC)}
}

func sequenceIDs(values ...string) func() string {
	index := 0
	return func() string { value := values[index]; index++; return value }
}

type discoveryStore struct {
	repositories port.DiscoveryRepositories
	transactions int
}

func (s *discoveryStore) DiscoveryView(ctx context.Context, fn func(context.Context, port.DiscoveryRepositories) error) error {
	return fn(ctx, s.repositories)
}
func (s *discoveryStore) DiscoveryTransact(ctx context.Context, fn func(context.Context, port.DiscoveryRepositories) error) error {
	s.transactions++
	return fn(ctx, s.repositories)
}

type storedDiscoveryBatch struct {
	hash            string
	acknowledgement port.DiscoveryBatchAcknowledgement
}
type discoveryRepository struct {
	candidates map[domain.DiscoveryCandidateID]domain.DiscoveryCandidate
	batches    map[string]storedDiscoveryBatch
}

func (r *discoveryRepository) ApplyBatch(_ context.Context, batch port.DiscoveryBatch) (port.DiscoveryBatchAcknowledgement, error) {
	key := string(batch.AgentID) + "/" + batch.ID
	if stored, ok := r.batches[key]; ok {
		if stored.hash != batch.RequestHash {
			return port.DiscoveryBatchAcknowledgement{}, port.ErrConflict
		}
		return stored.acknowledgement, nil
	}
	ack := port.DiscoveryBatchAcknowledgement{Accepted: len(batch.Candidates)}
	for _, incoming := range batch.Candidates {
		var existingID domain.DiscoveryCandidateID
		var existing domain.DiscoveryCandidate
		for id, candidate := range r.candidates {
			if candidate.Identity() == incoming.Identity() {
				existingID, existing = id, candidate
				break
			}
		}
		if existingID == "" {
			if !incoming.Present {
				continue
			}
			r.candidates[incoming.ID] = incoming.Clone()
			ack.Created++
			continue
		}
		if incoming.LastObservedAt.Before(existing.LastObservedAt) {
			continue
		}
		incoming.ID, incoming.CreatedAt, incoming.PromotedMonitorID = existing.ID, existing.CreatedAt, existing.PromotedMonitorID
		if existing.PromotedMonitorID != nil && (existing.Name != incoming.Name || !maps.Equal(existing.Labels, incoming.Labels)) {
			incoming.DriftHint = "source metadata changed after promotion"
		} else {
			incoming.DriftHint = existing.DriftHint
		}
		r.candidates[existingID] = incoming.Clone()
		ack.Updated++
	}
	r.batches[key] = storedDiscoveryBatch{hash: batch.RequestHash, acknowledgement: ack}
	return ack, nil
}
func (r *discoveryRepository) Get(_ context.Context, id domain.DiscoveryCandidateID) (domain.DiscoveryCandidate, error) {
	value, ok := r.candidates[id]
	if !ok {
		return domain.DiscoveryCandidate{}, port.ErrNotFound
	}
	return value.Clone(), nil
}
func (r *discoveryRepository) GetForUpdate(ctx context.Context, id domain.DiscoveryCandidateID) (domain.DiscoveryCandidate, error) {
	return r.Get(ctx, id)
}
func (r *discoveryRepository) List(_ context.Context, request port.DiscoveryListRequest) ([]domain.DiscoveryCandidate, error) {
	result := make([]domain.DiscoveryCandidate, 0, len(r.candidates))
	for _, value := range r.candidates {
		if request.After != "" && value.ID <= request.After {
			continue
		}
		if request.Filter.State == port.DiscoveryStatePromoted && value.PromotedMonitorID == nil ||
			request.Filter.State == port.DiscoveryStatePending && value.PromotedMonitorID != nil {
			continue
		}
		if request.Filter.Present != nil && value.Present != *request.Filter.Present {
			continue
		}
		result = append(result, value.Clone())
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	if request.Limit > 0 && len(result) > request.Limit {
		result = result[:request.Limit]
	}
	return result, nil
}
func (r *discoveryRepository) LinkPromotion(_ context.Context, id domain.DiscoveryCandidateID, monitorID domain.MonitorID, at time.Time) (bool, error) {
	candidate, ok := r.candidates[id]
	if !ok {
		return false, port.ErrNotFound
	}
	if candidate.PromotedMonitorID != nil {
		return false, nil
	}
	candidate.PromotedMonitorID = &monitorID
	candidate.UpdatedAt = at
	r.candidates[id] = candidate
	return true, nil
}
func (r *discoveryRepository) onlyCandidate() domain.DiscoveryCandidate {
	for _, value := range r.candidates {
		return value.Clone()
	}
	panic("no candidate")
}

type discoveryLocations struct{}

func (discoveryLocations) Create(context.Context, domain.Location) error { return nil }
func (discoveryLocations) Get(_ context.Context, id domain.LocationID) (domain.Location, error) {
	if id != "location-1" {
		return domain.Location{}, port.ErrNotFound
	}
	return domain.Location{ID: id, Name: "cluster", Enabled: true}, nil
}

type discoveryMonitors struct {
	monitors map[domain.MonitorID]domain.Monitor
}

func (r *discoveryMonitors) Create(_ context.Context, monitor domain.Monitor) error {
	if _, exists := r.monitors[monitor.ID]; exists {
		return port.ErrConflict
	}
	r.monitors[monitor.ID] = monitor
	return nil
}
func (r *discoveryMonitors) Get(_ context.Context, id domain.MonitorID) (domain.Monitor, error) {
	value, ok := r.monitors[id]
	if !ok {
		return domain.Monitor{}, port.ErrNotFound
	}
	return value, nil
}
func (*discoveryMonitors) AssignLocation(context.Context, port.MonitorLocation) error { return nil }
func (*discoveryMonitors) GetAssignment(context.Context, domain.MonitorID) (port.MonitorLocation, error) {
	return port.MonitorLocation{}, nil
}
func (*discoveryMonitors) ListDue(context.Context, time.Time, int) ([]port.DueMonitor, error) {
	return nil, nil
}
func (*discoveryMonitors) AdvanceNextRun(context.Context, domain.MonitorID, time.Time, time.Time) (bool, error) {
	return false, nil
}

type discoveryHealth struct {
	location domain.LocationHealth
	monitor  domain.MonitorHealth
}

func (*discoveryHealth) GetLocation(context.Context, domain.MonitorID, domain.LocationID) (domain.LocationHealth, error) {
	return domain.LocationHealth{}, nil
}
func (r *discoveryHealth) UpsertLocation(_ context.Context, value domain.LocationHealth) error {
	r.location = value
	return nil
}
func (*discoveryHealth) ListRequiredLocations(context.Context, domain.MonitorID) ([]domain.LocationHealth, error) {
	return nil, nil
}
func (*discoveryHealth) ListStale(context.Context, time.Time, int) ([]domain.LocationHealth, error) {
	return nil, nil
}
func (*discoveryHealth) ClaimStale(context.Context, domain.MonitorID, domain.LocationID, time.Time, time.Time) (bool, error) {
	return false, nil
}
func (*discoveryHealth) GetMonitor(context.Context, domain.MonitorID) (domain.MonitorHealth, error) {
	return domain.MonitorHealth{}, nil
}
func (r *discoveryHealth) UpsertMonitor(_ context.Context, value domain.MonitorHealth) error {
	r.monitor = value
	return nil
}
