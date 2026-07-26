package testdata

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	monitoringv1alpha1 "github.com/araihu/xisnove/operator/api/v1alpha1"
	"github.com/araihu/xisnove/operator/internal/controlplane"
)

// FakeControlPlane is a deterministic, stateful implementation of the narrow
// controller boundary. It deliberately retains only credential digests.
type FakeControlPlane struct {
	mu sync.Mutex

	monitors    map[string]*monitorRecord
	agents      map[string]*agentRecord
	nextMonitor int
	nextAgent   int

	monitorFailure error
}

type monitorRecord struct {
	owner   controlplane.OwnerReference
	state   controlplane.MonitorState
	spec    monitoringv1alpha1.MonitorSpec
	applies int
}

type agentRecord struct {
	owner       controlplane.OwnerReference
	state       controlplane.AgentState
	spec        monitoringv1alpha1.AgentSpec
	credentials map[int64][sha256.Size]byte
	applies     int
	puts        int
	revokes     []int64
}

type MonitorSnapshot struct {
	Owner   controlplane.OwnerReference
	State   controlplane.MonitorState
	Spec    monitoringv1alpha1.MonitorSpec
	Applies int
}

type AgentSnapshot struct {
	Owner                 controlplane.OwnerReference
	State                 controlplane.AgentState
	Spec                  monitoringv1alpha1.AgentSpec
	CredentialGenerations []int64
	Applies               int
	Puts                  int
	Revokes               []int64
}

func NewFakeControlPlane() *FakeControlPlane {
	return &FakeControlPlane{monitors: map[string]*monitorRecord{}, agents: map[string]*agentRecord{}}
}

func (f *FakeControlPlane) ApplyMonitor(_ context.Context, request controlplane.ApplyMonitorRequest) (controlplane.MonitorState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.monitorFailure != nil {
		return controlplane.MonitorState{}, f.monitorFailure
	}
	if record := f.monitors[request.Owner.Key]; record != nil {
		if record.owner.UID != request.Owner.UID || (request.ExternalID != "" && request.ExternalID != record.state.ExternalID) {
			return controlplane.MonitorState{}, controlplane.ErrOwnershipConflict
		}
		record.applies++
		record.spec = *request.Spec.DeepCopy()
		return record.state, nil
	}
	if request.ExternalID != "" {
		return controlplane.MonitorState{}, controlplane.ErrOwnershipConflict
	}
	f.nextMonitor++
	record := &monitorRecord{
		owner: request.Owner,
		state: controlplane.MonitorState{ExternalID: fmt.Sprintf("monitor-%d", f.nextMonitor), AggregateHealth: "pending"},
		spec:  *request.Spec.DeepCopy(), applies: 1,
	}
	f.monitors[request.Owner.Key] = record
	return record.state, nil
}

func (f *FakeControlPlane) DeleteMonitor(_ context.Context, request controlplane.DeleteRemoteObjectRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	record := f.monitors[request.Owner.Key]
	if record == nil {
		return controlplane.ErrNotFound
	}
	if record.owner.UID != request.Owner.UID || (request.ExternalID != "" && request.ExternalID != record.state.ExternalID) {
		return controlplane.ErrOwnershipConflict
	}
	delete(f.monitors, request.Owner.Key)
	return nil
}

func (f *FakeControlPlane) ApplyAgent(_ context.Context, request controlplane.ApplyAgentRequest) (controlplane.AgentState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if record := f.agents[request.Owner.Key]; record != nil {
		if record.owner.UID != request.Owner.UID || (request.ExternalID != "" && request.ExternalID != record.state.ExternalID) {
			return controlplane.AgentState{}, controlplane.ErrOwnershipConflict
		}
		if len(request.InitialCredential) != 0 && record.credentials[1] != sha256.Sum256(request.InitialCredential) {
			return controlplane.AgentState{}, controlplane.ErrCredentialConflict
		}
		record.applies++
		record.spec = *request.Spec.DeepCopy()
		return record.state, nil
	}
	if request.ExternalID != "" || len(request.InitialCredential) == 0 {
		return controlplane.AgentState{}, controlplane.ErrOwnershipConflict
	}
	f.nextAgent++
	record := &agentRecord{
		owner: request.Owner,
		state: controlplane.AgentState{ExternalID: fmt.Sprintf("agent-%d", f.nextAgent), CredentialGeneration: 1},
		spec:  *request.Spec.DeepCopy(), credentials: map[int64][sha256.Size]byte{1: sha256.Sum256(request.InitialCredential)}, applies: 1,
	}
	f.agents[request.Owner.Key] = record
	return record.state, nil
}

func (f *FakeControlPlane) ObserveAgent(_ context.Context, request controlplane.ObserveAgentRequest) (controlplane.AgentState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record := f.agents[request.Owner.Key]
	if record == nil {
		return controlplane.AgentState{}, controlplane.ErrNotFound
	}
	if record.owner.UID != request.Owner.UID || request.ExternalID != record.state.ExternalID {
		return controlplane.AgentState{}, controlplane.ErrOwnershipConflict
	}
	return record.state, nil
}

func (f *FakeControlPlane) PutAgentCredential(_ context.Context, request controlplane.PutAgentCredentialRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	record, err := f.agentFor(request.Owner, request.ExternalID)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(request.Credential)
	if existing, ok := record.credentials[request.Generation]; ok && existing != digest {
		return controlplane.ErrCredentialConflict
	}
	record.credentials[request.Generation] = digest
	if request.Generation > record.state.CredentialGeneration {
		record.state.CredentialGeneration = request.Generation
	}
	record.puts++
	return nil
}

func (f *FakeControlPlane) RevokeAgentCredential(_ context.Context, request controlplane.RevokeAgentCredentialRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	record, err := f.agentFor(request.Owner, request.ExternalID)
	if err != nil {
		return err
	}
	if record.state.PresentedCredentialGeneration <= request.Generation {
		return errors.New("replacement heartbeat not observed")
	}
	delete(record.credentials, request.Generation)
	record.revokes = append(record.revokes, request.Generation)
	return nil
}

func (f *FakeControlPlane) DeleteAgent(_ context.Context, request controlplane.DeleteRemoteObjectRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	record := f.agents[request.Owner.Key]
	if record == nil {
		return controlplane.ErrNotFound
	}
	if record.owner.UID != request.Owner.UID || (request.ExternalID != "" && request.ExternalID != record.state.ExternalID) {
		return controlplane.ErrOwnershipConflict
	}
	delete(f.agents, request.Owner.Key)
	return nil
}

func (f *FakeControlPlane) agentFor(owner controlplane.OwnerReference, externalID string) (*agentRecord, error) {
	record := f.agents[owner.Key]
	if record == nil {
		return nil, controlplane.ErrNotFound
	}
	if record.owner.UID != owner.UID || record.state.ExternalID != externalID {
		return nil, controlplane.ErrOwnershipConflict
	}
	return record, nil
}

func (f *FakeControlPlane) SetMonitorHealth(ownerKey, health string, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if record := f.monitors[ownerKey]; record != nil {
		record.state.AggregateHealth, record.state.HealthLastTransitionAt = health, at
	}
}

func (f *FakeControlPlane) SetMonitorFailure(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.monitorFailure = err
}

func (f *FakeControlPlane) SetAgentObservation(ownerKey string, presented int64, heartbeat, discovery time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if record := f.agents[ownerKey]; record != nil {
		record.state.PresentedCredentialGeneration = presented
		record.state.LastHeartbeatAt = heartbeat
		record.state.LastDiscoverySyncAt = discovery
	}
}

func (f *FakeControlPlane) SeedMonitor(owner controlplane.OwnerReference, externalID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.monitors[owner.Key] = &monitorRecord{owner: owner, state: controlplane.MonitorState{ExternalID: externalID, AggregateHealth: "pending"}, applies: 1}
}

func (f *FakeControlPlane) Monitor(ownerKey string) (MonitorSnapshot, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record := f.monitors[ownerKey]
	if record == nil {
		return MonitorSnapshot{}, false
	}
	return MonitorSnapshot{Owner: record.owner, State: record.state, Spec: *record.spec.DeepCopy(), Applies: record.applies}, true
}

func (f *FakeControlPlane) Agent(ownerKey string) (AgentSnapshot, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record := f.agents[ownerKey]
	if record == nil {
		return AgentSnapshot{}, false
	}
	generations := make([]int64, 0, len(record.credentials))
	for generation := range record.credentials {
		generations = append(generations, generation)
	}
	return AgentSnapshot{Owner: record.owner, State: record.state, Spec: *record.spec.DeepCopy(), CredentialGenerations: generations, Applies: record.applies, Puts: record.puts, Revokes: append([]int64(nil), record.revokes...)}, true
}
