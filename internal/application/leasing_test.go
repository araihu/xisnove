package application_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/araihu/xisnove/internal/application"
	"github.com/araihu/xisnove/internal/domain"
)

func TestExpiredLeaseCanBeReclaimedByCompatibleAgent(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	store := newWorkStore(now)
	store.agents["a1"] = activeHTTPAgent("a1", "location-1")
	store.agents["a2"] = activeHTTPAgent("a2", "location-1")
	store.runs["run-1"] = application.RunRecord{
		ID:           "run-1",
		MonitorID:    "monitor-1",
		LocationID:   "location-1",
		ScheduledFor: now,
		Probe: domain.ProbeDefinition{
			Kind: domain.MonitorKindHTTP,
			HTTP: domain.HTTPProbe{Method: "GET", URL: "https://example.com/health"},
		},
		Timeout: 5 * time.Second,
		Status:  "available",
	}
	service := application.NewLeaseService(application.LeaseServiceConfig{
		Store:         store,
		Tokens:        &leaseTokenIssuer{},
		LeaseDuration: 30 * time.Second,
	})

	first, err := service.LeaseHTTP(context.Background(), "a1", 0)
	if err != nil {
		t.Fatal(err)
	}
	store.now = store.now.Add(31 * time.Second)
	second, err := service.LeaseHTTP(context.Background(), "a2", 0)
	if err != nil {
		t.Fatal(err)
	}
	if first.RunID != second.RunID || first.LeaseToken == second.LeaseToken {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	if store.runs["run-1"].LeaseAttempt != 2 {
		t.Fatalf("LeaseAttempt = %d", store.runs["run-1"].LeaseAttempt)
	}
}

func TestLeaseHTTPRejectsAgentWithoutHTTPCapability(t *testing.T) {
	store := newWorkStore(time.Now())
	store.agents["a1"] = application.AgentRecord{Agent: domain.Agent{
		ID: "a1", LocationID: "location-1",
		Capabilities: []domain.AgentCapability{"kubernetes-discovery"},
	}}
	service := application.NewLeaseService(application.LeaseServiceConfig{
		Store: store, Tokens: &leaseTokenIssuer{}, LeaseDuration: 30 * time.Second,
	})

	_, err := service.LeaseHTTP(context.Background(), "a1", 0)
	if !errors.Is(err, application.ErrInvalidCredentials) {
		t.Fatalf("error = %v", err)
	}
}

type workStore struct {
	now    time.Time
	due    map[domain.MonitorID]application.DueMonitor
	runs   map[domain.CheckRunID]application.RunRecord
	agents map[domain.AgentID]application.AgentRecord
}

func newWorkStore(now time.Time) *workStore {
	return &workStore{
		now:    now,
		due:    map[domain.MonitorID]application.DueMonitor{},
		runs:   map[domain.CheckRunID]application.RunRecord{},
		agents: map[domain.AgentID]application.AgentRecord{},
	}
}

func (s *workStore) Repositories() application.Repositories {
	return application.Repositories{
		Monitors: &workMonitorRepository{store: s},
		Runs:     &workRunRepository{store: s},
		Agents:   &workAgentRepository{store: s},
	}
}

func (s *workStore) WithinTx(
	_ context.Context,
	fn func(application.Repositories) error,
) error {
	return fn(s.Repositories())
}

type workMonitorRepository struct {
	store *workStore
}

func (*workMonitorRepository) Create(context.Context, domain.Monitor) error {
	return errors.New("not implemented")
}

func (*workMonitorRepository) Get(
	context.Context,
	domain.MonitorID,
) (domain.Monitor, error) {
	return domain.Monitor{}, application.ErrNotFound
}

func (*workMonitorRepository) AssignLocation(
	context.Context,
	application.MonitorLocation,
) error {
	return errors.New("not implemented")
}

func (*workMonitorRepository) GetAssignment(
	context.Context,
	domain.MonitorID,
) (application.MonitorLocation, error) {
	return application.MonitorLocation{}, application.ErrNotFound
}

func (r *workMonitorRepository) ListDue(
	_ context.Context,
	now time.Time,
	limit int,
) ([]application.DueMonitor, error) {
	due := make([]application.DueMonitor, 0, limit)
	for _, monitor := range r.store.due {
		if !monitor.NextRunAt.After(now) && len(due) < limit {
			due = append(due, monitor)
		}
	}
	return due, nil
}

func (r *workMonitorRepository) AdvanceNextRun(
	_ context.Context,
	monitorID domain.MonitorID,
	nextRunAt time.Time,
	_ time.Time,
) (bool, error) {
	monitor, ok := r.store.due[monitorID]
	if !ok || !monitor.NextRunAt.Before(nextRunAt) {
		return false, nil
	}
	monitor.NextRunAt = nextRunAt
	monitor.Monitor.NextRunAt = nextRunAt
	r.store.due[monitorID] = monitor
	return true, nil
}

type workRunRepository struct {
	store *workStore
}

func (r *workRunRepository) DatabaseNow(context.Context) (time.Time, error) {
	return r.store.now, nil
}

func (r *workRunRepository) Insert(
	_ context.Context,
	record application.NewRunRecord,
) (bool, error) {
	for _, existing := range r.store.runs {
		if existing.MonitorID == record.MonitorID &&
			existing.LocationID == record.LocationID &&
			existing.ScheduledFor.Equal(record.ScheduledFor) {
			return false, nil
		}
	}
	r.store.runs[record.ID] = application.RunRecord{
		ID:           record.ID,
		MonitorID:    record.MonitorID,
		LocationID:   record.LocationID,
		ScheduledFor: record.ScheduledFor,
		Probe:        record.Probe,
		Timeout:      record.Timeout,
		Status:       "available",
	}
	return true, nil
}

func (r *workRunRepository) ClaimHTTP(
	_ context.Context,
	params application.ClaimRunParams,
) (application.RunRecord, error) {
	agent := r.store.agents[params.AgentID].Agent
	for id, run := range r.store.runs {
		expired := run.LeaseExpiresAt != nil && !run.LeaseExpiresAt.After(params.Now)
		if run.LocationID != agent.LocationID ||
			(run.Status != "available" && !(run.Status == "leased" && expired)) {
			continue
		}
		run.Status = "leased"
		run.LeaseAgentID = params.AgentID
		run.LeaseTokenHash = bytes.Clone(params.LeaseTokenHash)
		run.LeaseAttempt++
		expiresAt := params.LeaseExpiresAt
		run.LeaseExpiresAt = &expiresAt
		r.store.runs[id] = run
		return run, nil
	}
	return application.RunRecord{}, application.ErrNotFound
}

func (r *workRunRepository) Get(
	_ context.Context,
	id domain.CheckRunID,
) (application.RunRecord, error) {
	run, ok := r.store.runs[id]
	if !ok {
		return application.RunRecord{}, application.ErrNotFound
	}
	return run, nil
}

func (r *workRunRepository) Resolve(
	_ context.Context,
	runID domain.CheckRunID,
	agentID domain.AgentID,
	leaseTokenHash []byte,
	resolvedAt time.Time,
) (bool, error) {
	run, ok := r.store.runs[runID]
	if !ok ||
		run.LeaseAgentID != agentID ||
		!bytes.Equal(run.LeaseTokenHash, leaseTokenHash) {
		return false, nil
	}
	run.Status = "resolved"
	run.ResolvedAt = &resolvedAt
	r.store.runs[runID] = run
	return true, nil
}

type workAgentRepository struct {
	store *workStore
}

func (*workAgentRepository) CreateEnrollmentToken(
	context.Context,
	application.EnrollmentTokenRecord,
) error {
	return errors.New("not implemented")
}

func (*workAgentRepository) ConsumeEnrollmentToken(
	context.Context,
	[]byte,
	time.Time,
	time.Time,
) (application.EnrollmentTokenRecord, bool, error) {
	return application.EnrollmentTokenRecord{}, false, errors.New("not implemented")
}

func (r *workAgentRepository) Create(
	_ context.Context,
	record application.AgentRecord,
) error {
	r.store.agents[record.Agent.ID] = record
	return nil
}

func (r *workAgentRepository) Get(
	_ context.Context,
	id domain.AgentID,
) (application.AgentRecord, error) {
	record, ok := r.store.agents[id]
	if !ok {
		return application.AgentRecord{}, application.ErrNotFound
	}
	return record, nil
}

func (*workAgentRepository) FindActiveByCredentialHash(
	context.Context,
	[]byte,
) (application.AgentRecord, error) {
	return application.AgentRecord{}, application.ErrNotFound
}

func (*workAgentRepository) UpdateHeartbeat(
	context.Context,
	domain.AgentID,
	uint64,
	string,
	[]domain.AgentCapability,
	time.Time,
) (bool, error) {
	return false, errors.New("not implemented")
}

func activeHTTPAgent(id domain.AgentID, locationID domain.LocationID) application.AgentRecord {
	return application.AgentRecord{Agent: domain.Agent{
		ID: id, LocationID: locationID,
		Capabilities:         []domain.AgentCapability{domain.CapabilityHTTP},
		CredentialGeneration: 1,
	}}
}

type leaseTokenIssuer struct {
	next int
}

func (i *leaseTokenIssuer) New() (application.IssuedToken, error) {
	i.next++
	raw := fmt.Sprintf("lease-token-%d", i.next)
	hash := []byte(fmt.Sprintf("lease-hash-%d", i.next))
	return application.IssuedToken{Raw: raw, Hash: hash}, nil
}

func (*leaseTokenIssuer) Hash(raw string) []byte {
	return []byte("hash:" + raw)
}
