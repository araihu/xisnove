package testdata

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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
	idempotency map[string][sha256.Size]byte

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
	return &FakeControlPlane{monitors: map[string]*monitorRecord{}, agents: map[string]*agentRecord{}, idempotency: map[string][sha256.Size]byte{}}
}

func (f *FakeControlPlane) ApplyMonitor(_ context.Context, request controlplane.ApplyMonitorRequest) (controlplane.MonitorState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fingerprintRequest := monitorApplyFingerprint(request)
	replay, err := f.checkIdempotency("apply-monitor", request.IdempotencyKey, fingerprintRequest)
	if err != nil {
		return controlplane.MonitorState{}, err
	}
	if f.monitorFailure != nil {
		return controlplane.MonitorState{}, f.monitorFailure
	}
	if record := f.monitors[request.Owner.Key]; record != nil {
		if record.owner.UID != request.Owner.UID || (request.ExternalID != "" && request.ExternalID != record.state.ExternalID) {
			return controlplane.MonitorState{}, controlplane.ErrOwnershipConflict
		}
		if replay {
			return record.state, nil
		}
		record.applies++
		record.spec = *request.Spec.DeepCopy()
		f.recordIdempotency("apply-monitor", request.IdempotencyKey, fingerprintRequest)
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
	f.recordIdempotency("apply-monitor", request.IdempotencyKey, fingerprintRequest)
	return record.state, nil
}

func (f *FakeControlPlane) DeleteMonitor(_ context.Context, request controlplane.DeleteRemoteObjectRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	replay, err := f.checkIdempotency("delete-monitor", request.IdempotencyKey, request)
	if err != nil {
		return err
	}
	if replay {
		return nil
	}
	record := f.monitors[request.Owner.Key]
	if record == nil {
		return controlplane.ErrNotFound
	}
	if record.owner.UID != request.Owner.UID || (request.ExternalID != "" && request.ExternalID != record.state.ExternalID) {
		return controlplane.ErrOwnershipConflict
	}
	delete(f.monitors, request.Owner.Key)
	f.recordIdempotency("delete-monitor", request.IdempotencyKey, request)
	return nil
}

func (f *FakeControlPlane) ApplyAgent(_ context.Context, request controlplane.ApplyAgentRequest) (controlplane.AgentState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fingerprintRequest := agentApplyFingerprint(request)
	replay, err := f.checkIdempotency("apply-agent", request.IdempotencyKey, fingerprintRequest)
	if err != nil {
		return controlplane.AgentState{}, err
	}
	if record := f.agents[request.Owner.Key]; record != nil {
		if record.owner.UID != request.Owner.UID || (request.ExternalID != "" && request.ExternalID != record.state.ExternalID) {
			return controlplane.AgentState{}, controlplane.ErrOwnershipConflict
		}
		if len(request.InitialCredential) != 0 && record.credentials[1] != sha256.Sum256(request.InitialCredential) {
			return controlplane.AgentState{}, controlplane.ErrCredentialConflict
		}
		if replay {
			return record.state, nil
		}
		record.applies++
		record.spec = *request.Spec.DeepCopy()
		f.recordIdempotency("apply-agent", request.IdempotencyKey, fingerprintRequest)
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
	f.recordIdempotency("apply-agent", request.IdempotencyKey, fingerprintRequest)
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
	fingerprintRequest := credentialFingerprint(request.Owner, request.ExternalID, request.Generation, request.Credential)
	replay, err := f.checkIdempotency("put-agent-credential", request.IdempotencyKey, fingerprintRequest)
	if err != nil {
		return err
	}
	if replay {
		return nil
	}
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
	f.recordIdempotency("put-agent-credential", request.IdempotencyKey, fingerprintRequest)
	return nil
}

func (f *FakeControlPlane) RevokeAgentCredential(_ context.Context, request controlplane.RevokeAgentCredentialRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	replay, err := f.checkIdempotency("revoke-agent-credential", request.IdempotencyKey, request)
	if err != nil {
		return err
	}
	if replay {
		return nil
	}
	record, err := f.agentFor(request.Owner, request.ExternalID)
	if err != nil {
		return err
	}
	if record.state.PresentedCredentialGeneration <= request.Generation {
		return errors.New("replacement heartbeat not observed")
	}
	delete(record.credentials, request.Generation)
	record.revokes = append(record.revokes, request.Generation)
	f.recordIdempotency("revoke-agent-credential", request.IdempotencyKey, request)
	return nil
}

func (f *FakeControlPlane) DeleteAgent(_ context.Context, request controlplane.DeleteRemoteObjectRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	replay, err := f.checkIdempotency("delete-agent", request.IdempotencyKey, request)
	if err != nil {
		return err
	}
	if replay {
		return nil
	}
	record := f.agents[request.Owner.Key]
	if record == nil {
		return controlplane.ErrNotFound
	}
	if record.owner.UID != request.Owner.UID || (request.ExternalID != "" && request.ExternalID != record.state.ExternalID) {
		return controlplane.ErrOwnershipConflict
	}
	delete(f.agents, request.Owner.Key)
	f.recordIdempotency("delete-agent", request.IdempotencyKey, request)
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

func (f *FakeControlPlane) SeedAgent(owner controlplane.OwnerReference, externalID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agents[owner.Key] = &agentRecord{
		owner:       owner,
		state:       controlplane.AgentState{ExternalID: externalID, CredentialGeneration: 1},
		credentials: map[int64][sha256.Size]byte{1: sha256.Sum256([]byte("seeded-digest-input"))},
	}
}

func (f *FakeControlPlane) checkIdempotency(operation, key string, request any) (bool, error) {
	if key == "" {
		return false, errors.New("idempotency key is required")
	}
	digest := requestDigest(request)
	existing, found := f.idempotency[operation+"\x00"+key]
	if !found {
		return false, nil
	}
	if existing != digest {
		return false, errors.New("idempotency key was reused with a different request")
	}
	return true, nil
}

func (f *FakeControlPlane) recordIdempotency(operation, key string, request any) {
	f.idempotency[operation+"\x00"+key] = requestDigest(request)
}

func requestDigest(request any) [sha256.Size]byte {
	encoded, err := json.Marshal(request)
	if err != nil {
		panic(fmt.Sprintf("fake control-plane request cannot be fingerprinted: %v", err))
	}
	return sha256.Sum256(encoded)
}

type agentApplyFingerprintValue struct {
	Owner            controlplane.OwnerReference
	Name             string
	Spec             monitoringv1alpha1.AgentSpec
	CredentialDigest [sha256.Size]byte
	HasCredential    bool
}

func agentApplyFingerprint(request controlplane.ApplyAgentRequest) agentApplyFingerprintValue {
	return agentApplyFingerprintValue{
		Owner: request.Owner, Name: request.Name, Spec: request.Spec,
		CredentialDigest: sha256.Sum256(request.InitialCredential), HasCredential: len(request.InitialCredential) != 0,
	}
}

type monitorApplyFingerprintValue struct {
	Owner controlplane.OwnerReference
	Name  string
	Spec  monitoringv1alpha1.MonitorSpec
}

func monitorApplyFingerprint(request controlplane.ApplyMonitorRequest) monitorApplyFingerprintValue {
	return monitorApplyFingerprintValue{Owner: request.Owner, Name: request.Name, Spec: request.Spec}
}

type credentialFingerprintValue struct {
	Owner      controlplane.OwnerReference
	ExternalID string
	Generation int64
	Digest     [sha256.Size]byte
}

func credentialFingerprint(owner controlplane.OwnerReference, externalID string, generation int64, credential []byte) credentialFingerprintValue {
	return credentialFingerprintValue{Owner: owner, ExternalID: externalID, Generation: generation, Digest: sha256.Sum256(credential)}
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
