package application

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
	"github.com/google/uuid"
)

const (
	operatorMonitorKind        = "monitor"
	operatorAgentKind          = "agent"
	operatorMaxCredentialBytes = 1024
	operatorMinCredentialBytes = 32
)

// CredentialHasher is deliberately narrower than TokenIssuer. Operator
// credentials are supplied by the caller and must never be minted, returned,
// or retained by the control plane.
type CredentialHasher interface{ Hash(string) []byte }

type OperatorService struct {
	Store       port.UnitOfWork
	Credentials CredentialHasher
}

type ApplyOperatorMonitor struct {
	Owner          port.ExternalOwner
	Monitor        ReplaceMonitorCommand
	IdempotencyKey string
}

type DeleteOperatorMonitor struct {
	Owner      port.ExternalOwner
	ExternalID domain.MonitorID
}

type OperatorMonitorState struct {
	ExternalID       domain.MonitorID
	State            domain.HealthState
	LastTransitionAt time.Time
	LastObservedAt   time.Time
}

type OperatorInitialCredential struct {
	Generation uint64
	Credential string
}

type ApplyOperatorAgent struct {
	Owner             port.ExternalOwner
	Name              string
	LocationID        domain.LocationID
	Enabled           bool
	Capabilities      []domain.AgentCapability
	InitialCredential OperatorInitialCredential
	IdempotencyKey    string
}

type OperatorAgentState struct {
	ExternalID           domain.AgentID
	CredentialGeneration uint64
	LastSeenAt           time.Time
	LastDiscoveryAt      time.Time
}

type PutOperatorCredential struct {
	Owner          port.ExternalOwner
	AgentID        domain.AgentID
	Generation     uint64
	Credential     string
	IdempotencyKey string
}

type RevokeOperatorCredential struct {
	Owner      port.ExternalOwner
	AgentID    domain.AgentID
	Generation uint64
}

type DeleteOperatorAgent struct {
	Owner      port.ExternalOwner
	ExternalID domain.AgentID
}

func (s OperatorService) ApplyMonitor(ctx context.Context, request ApplyOperatorMonitor) (OperatorMonitorState, error) {
	if err := s.ready(false); err != nil {
		return OperatorMonitorState{}, err
	}
	if err := validateOperatorOwner(request.Owner); err != nil {
		return OperatorMonitorState{}, err
	}
	if err := validateOperatorKey(request.IdempotencyKey); err != nil {
		return OperatorMonitorState{}, err
	}
	resourceID := domain.MonitorID(uuid.NewString())
	result, err := NewIdempotencyService[OperatorMonitorState](s.Store).Execute(ctx, IdempotencyRequest{
		Principal: operatorPrincipal(request.Owner), OperationID: "applyOperatorMonitor", Key: request.IdempotencyKey,
		Request: struct {
			Owner   port.ExternalOwner
			Monitor ReplaceMonitorCommand
		}{request.Owner, request.Monitor}, ResourceKind: operatorMonitorKind,
	}, func(ctx context.Context, repositories Repositories) (string, OperatorMonitorState, error) {
		if err := requireOperatorMonitorRepositories(repositories); err != nil {
			return "", OperatorMonitorState{}, err
		}
		binding, err := repositories.Operator.Resolve(ctx, request.Owner, operatorMonitorKind)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return "", OperatorMonitorState{}, err
		}
		newBinding := errors.Is(err, ErrNotFound)
		id := resourceID
		if !newBinding {
			id = domain.MonitorID(binding.ResourceID)
		}
		now, err := operatorDatabaseNow(ctx, repositories)
		if err != nil {
			return "", OperatorMonitorState{}, err
		}
		if newBinding {
			monitor, buildErr := newConfiguredMonitor(id, request.Monitor.CreateMonitorCommand, now)
			if buildErr != nil {
				return "", OperatorMonitorState{}, validationField("monitor", "contains invalid configuration")
			}
			monitor.Enabled = request.Monitor.Enabled
			if _, locationErr := repositories.Locations.Get(ctx, request.Monitor.LocationID); locationErr != nil {
				return "", OperatorMonitorState{}, locationErr
			}
			if createErr := repositories.Monitors.Create(ctx, monitor); createErr != nil {
				return "", OperatorMonitorState{}, createErr
			}
			if assignErr := repositories.Monitors.AssignLocation(ctx, MonitorLocation{MonitorID: id, LocationID: request.Monitor.LocationID, Required: request.Monitor.RequiredLocation}); assignErr != nil {
				return "", OperatorMonitorState{}, assignErr
			}
			if healthErr := repositories.Health.UpsertLocation(ctx, domain.LocationHealth{MonitorID: id, LocationID: request.Monitor.LocationID, State: domain.HealthPending, LastTransitionAt: now}); healthErr != nil {
				return "", OperatorMonitorState{}, healthErr
			}
			if healthErr := repositories.Health.UpsertMonitor(ctx, domain.MonitorHealth{MonitorID: id, State: domain.HealthPending, LastTransitionAt: now}); healthErr != nil {
				return "", OperatorMonitorState{}, healthErr
			}
		} else {
			current, getErr := repositories.Management.GetMonitor(ctx, id)
			if getErr != nil {
				return "", OperatorMonitorState{}, getErr
			}
			monitor, buildErr := newConfiguredMonitor(id, request.Monitor.CreateMonitorCommand, current.Monitor.CreatedAt)
			if buildErr != nil {
				return "", OperatorMonitorState{}, validationField("monitor", "contains invalid configuration")
			}
			monitor.Enabled, monitor.NextRunAt, monitor.UpdatedAt = request.Monitor.Enabled, current.Monitor.NextRunAt, now
			updated, updateErr := repositories.ManagementCommands.ReplaceMonitor(ctx, port.MonitorRecord{Monitor: monitor, LocationID: request.Monitor.LocationID, RequiredLocation: request.Monitor.RequiredLocation})
			if updateErr != nil {
				return "", OperatorMonitorState{}, updateErr
			}
			if !updated {
				return "", OperatorMonitorState{}, ErrConflict
			}
		}
		if bindErr := repositories.Operator.Bind(ctx, port.OperatorBinding{Owner: request.Owner, Kind: operatorMonitorKind, ResourceID: string(id)}); bindErr != nil {
			return "", OperatorMonitorState{}, bindErr
		}
		state, stateErr := operatorMonitorState(ctx, repositories, id)
		return string(id), state, stateErr
	}, func(ctx context.Context, repositories Repositories, resourceID string) (OperatorMonitorState, error) {
		return operatorMonitorState(ctx, repositories, domain.MonitorID(resourceID))
	})
	if err != nil {
		return OperatorMonitorState{}, fmt.Errorf("apply operator monitor: %w", err)
	}
	return result, nil
}

func (s OperatorService) DeleteMonitor(ctx context.Context, request DeleteOperatorMonitor) error {
	if err := s.ready(false); err != nil {
		return err
	}
	if err := validateOperatorOwner(request.Owner); err != nil {
		return err
	}
	return s.Store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		if repositories.Operator == nil || repositories.ManagementCommands == nil || repositories.Runs == nil {
			return errors.New("operator monitor repositories are not configured")
		}
		binding, err := repositories.Operator.Resolve(ctx, request.Owner, operatorMonitorKind)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if request.ExternalID != "" && request.ExternalID != domain.MonitorID(binding.ResourceID) {
			return ErrConflict
		}
		now, err := operatorDatabaseNow(ctx, repositories)
		if err != nil {
			return err
		}
		if _, err := repositories.ManagementCommands.DisableMonitor(ctx, domain.MonitorID(binding.ResourceID), now); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		return repositories.Operator.Tombstone(ctx, request.Owner, operatorMonitorKind, now)
	})
}

func (s OperatorService) ApplyAgent(ctx context.Context, request ApplyOperatorAgent) (OperatorAgentState, error) {
	if err := s.ready(true); err != nil {
		return OperatorAgentState{}, err
	}
	if err := validateOperatorOwner(request.Owner); err != nil {
		return OperatorAgentState{}, err
	}
	if err := validateOperatorKey(request.IdempotencyKey); err != nil {
		return OperatorAgentState{}, err
	}
	if request.InitialCredential.Generation != 1 {
		return OperatorAgentState{}, validationField("initialCredential.generation", "must equal one")
	}
	if err := validateOperatorCredential(request.InitialCredential.Credential); err != nil {
		return OperatorAgentState{}, err
	}
	resourceID := domain.AgentID(uuid.NewString())
	var result OperatorAgentState
	err := s.Store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		if err := requireOperatorAgentRepositories(repositories); err != nil {
			return err
		}
		// Keep hashing in this transaction: neither a raw credential nor its hash
		// is retained if resource creation, binding, or idempotency rolls back.
		hash := slices.Clone(s.Credentials.Hash(request.InitialCredential.Credential))
		defer clearBytes(hash)
		fingerprint, err := CanonicalRequestFingerprint(struct {
			Owner          port.ExternalOwner
			Name           string
			LocationID     domain.LocationID
			Enabled        bool
			Capabilities   []domain.AgentCapability
			Generation     uint64
			CredentialHash []byte
		}{request.Owner, request.Name, request.LocationID, request.Enabled, request.Capabilities, request.InitialCredential.Generation, hash})
		if err != nil {
			return err
		}
		now, err := operatorDatabaseNow(ctx, repositories)
		if err != nil {
			return err
		}
		record, err := repositories.Idempotency.Get(ctx, operatorPrincipal(request.Owner).CredentialID, "applyOperatorAgent", request.IdempotencyKey, now)
		if err == nil {
			if record.RequestHash != fingerprint || record.ResourceKind != operatorAgentKind {
				return ErrIdempotencyKeyReused
			}
			result, err = operatorAgentState(ctx, repositories, domain.AgentID(record.ResourceID))
			return err
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		binding, resolveErr := repositories.Operator.Resolve(ctx, request.Owner, operatorAgentKind)
		if resolveErr != nil && !errors.Is(resolveErr, ErrNotFound) {
			return resolveErr
		}
		id := resourceID
		if resolveErr == nil {
			id = domain.AgentID(binding.ResourceID)
		}
		if resolveErr == nil {
			agent, getErr := repositories.Management.GetAgent(ctx, id)
			if getErr != nil {
				return getErr
			}
			if agent.RevokedAt != nil {
				return ErrConflict
			}
			initial, credentialErr := repositories.ManagementCommands.GetAgentCredentialGeneration(ctx, id, 1)
			if credentialErr != nil || subtle.ConstantTimeCompare(initial.CredentialHash, hash) != 1 {
				return ErrConflict
			}
			agent.Name, agent.LocationID, agent.Capabilities, agent.UpdatedAt = strings.TrimSpace(request.Name), request.LocationID, slices.Clone(request.Capabilities), now
			if agent.Name == "" || domain.ValidateAgentCapabilities(agent.Capabilities) != nil {
				return validationField("agent", "contains invalid configuration")
			}
			updated, updateErr := repositories.ManagementCommands.UpdateAgent(ctx, agent)
			if updateErr != nil {
				return updateErr
			}
			if !updated {
				return ErrConflict
			}
		} else {
			if !request.Enabled {
				return validationField("enabled", "must be true when creating an agent")
			}
			if _, locationErr := repositories.Locations.Get(ctx, request.LocationID); locationErr != nil {
				return locationErr
			}
			agent, createErr := domain.NewAgent(domain.NewAgentParams{ID: id, LocationID: request.LocationID, Name: request.Name, Capabilities: request.Capabilities, CredentialGeneration: 1, CreatedAt: now})
			if createErr != nil {
				return validationField("agent", "contains invalid configuration")
			}
			if createErr := repositories.Agents.Create(ctx, AgentRecord{Agent: agent, CredentialHash: slices.Clone(hash)}); createErr != nil {
				return createErr
			}
		}
		if bindErr := repositories.Operator.Bind(ctx, port.OperatorBinding{Owner: request.Owner, Kind: operatorAgentKind, ResourceID: string(id)}); bindErr != nil {
			return bindErr
		}
		if createErr := repositories.Idempotency.Create(ctx, port.IdempotencyRecord{PrincipalID: operatorPrincipal(request.Owner).CredentialID, OperationID: "applyOperatorAgent", Key: request.IdempotencyKey, RequestHash: fingerprint, ResourceKind: operatorAgentKind, ResourceID: string(id), CreatedAt: now, ExpiresAt: now.Add(DefaultIdempotencyLifetime)}); createErr != nil {
			if errors.Is(createErr, ErrConflict) {
				return ErrConflict
			}
			return createErr
		}
		result, err = operatorAgentState(ctx, repositories, id)
		return err
	})
	if err != nil {
		return OperatorAgentState{}, fmt.Errorf("apply operator agent: %w", err)
	}
	return result, nil
}

func (s OperatorService) PutAgentCredential(ctx context.Context, request PutOperatorCredential) error {
	if err := s.ready(true); err != nil {
		return err
	}
	if err := validateOperatorOwner(request.Owner); err != nil {
		return err
	}
	if err := validateOperatorKey(request.IdempotencyKey); err != nil {
		return err
	}
	if request.AgentID == "" || request.Generation < 2 {
		return validationField("generation", "must be a later credential generation")
	}
	if err := validateOperatorCredential(request.Credential); err != nil {
		return err
	}
	return s.Store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		if err := requireOperatorAgentRepositories(repositories); err != nil {
			return err
		}
		hash := slices.Clone(s.Credentials.Hash(request.Credential))
		defer clearBytes(hash)
		binding, err := repositories.Operator.Resolve(ctx, request.Owner, operatorAgentKind)
		if err != nil {
			return err
		}
		if domain.AgentID(binding.ResourceID) != request.AgentID {
			return ErrConflict
		}
		agent, err := repositories.Management.GetAgent(ctx, request.AgentID)
		if err != nil {
			return err
		}
		if agent.RevokedAt != nil {
			return ErrConflict
		}
		if request.Generation != agent.CredentialGeneration+1 {
			existing, getErr := repositories.ManagementCommands.GetAgentCredentialGeneration(ctx, request.AgentID, request.Generation)
			if getErr == nil && existing.RevokedAt == nil && subtle.ConstantTimeCompare(existing.CredentialHash, hash) == 1 {
				return nil
			}
			return ErrConflict
		}
		now, err := operatorDatabaseNow(ctx, repositories)
		if err != nil {
			return err
		}
		created, err := repositories.ManagementCommands.CreateAgentCredentialGeneration(ctx, port.CreateAgentCredentialGenerationCommand{ExpectedCurrentGeneration: agent.CredentialGeneration, Credential: port.AgentCredentialRecord{AgentID: request.AgentID, Generation: request.Generation, CredentialHash: slices.Clone(hash), CreatedAt: now}})
		if err != nil {
			return err
		}
		if !created {
			return ErrConflict
		}
		return nil
	})
}

func (s OperatorService) RevokeAgentCredential(ctx context.Context, request RevokeOperatorCredential) error {
	if err := s.ready(false); err != nil {
		return err
	}
	if err := validateOperatorOwner(request.Owner); err != nil {
		return err
	}
	if request.AgentID == "" || request.Generation == 0 {
		return validationField("credential", "agent and generation are required")
	}
	return s.Store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		if repositories.Operator == nil || repositories.ManagementCommands == nil || repositories.Runs == nil {
			return errors.New("operator credential repositories are not configured")
		}
		binding, err := repositories.Operator.Resolve(ctx, request.Owner, operatorAgentKind)
		if err != nil {
			return err
		}
		if domain.AgentID(binding.ResourceID) != request.AgentID {
			return ErrConflict
		}
		now, err := operatorDatabaseNow(ctx, repositories)
		if err != nil {
			return err
		}
		outcome, err := repositories.ManagementCommands.RevokeAgentCredentialGeneration(ctx, request.AgentID, request.Generation, now)
		if err != nil {
			return err
		}
		switch outcome {
		case port.CredentialGenerationRevoked, port.CredentialGenerationAlreadyRevoked:
			return nil
		case port.CredentialGenerationCurrent, port.CredentialGenerationReplacementUnobserved:
			return ErrConflict
		default:
			return ErrNotFound
		}
	})
}

func (s OperatorService) DeleteAgent(ctx context.Context, request DeleteOperatorAgent) error {
	if err := s.ready(false); err != nil {
		return err
	}
	if err := validateOperatorOwner(request.Owner); err != nil {
		return err
	}
	return s.Store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		if repositories.Operator == nil || repositories.ManagementCommands == nil || repositories.Runs == nil {
			return errors.New("operator agent repositories are not configured")
		}
		binding, err := repositories.Operator.Resolve(ctx, request.Owner, operatorAgentKind)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if request.ExternalID != "" && request.ExternalID != domain.AgentID(binding.ResourceID) {
			return ErrConflict
		}
		now, err := operatorDatabaseNow(ctx, repositories)
		if err != nil {
			return err
		}
		if _, err := repositories.ManagementCommands.RevokeAgent(ctx, domain.AgentID(binding.ResourceID), now); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		return repositories.Operator.Tombstone(ctx, request.Owner, operatorAgentKind, now)
	})
}

func (s OperatorService) ready(credentials bool) error {
	if s.Store == nil {
		return errors.New("operator service is not configured")
	}
	if credentials && s.Credentials == nil {
		return errors.New("operator credential hasher is not configured")
	}
	return nil
}

func validateOperatorOwner(owner port.ExternalOwner) error {
	if strings.TrimSpace(owner.Key) == "" || strings.TrimSpace(owner.UID) == "" || len(owner.Key) > 512 || len(owner.UID) > 253 {
		return validationField("owner", "key and uid are required")
	}
	return nil
}
func validateOperatorKey(key string) error {
	if strings.TrimSpace(key) == "" || len([]byte(key)) > MaxIdempotencyKeyBytes {
		return validationField("idempotencyKey", "is required")
	}
	return nil
}
func validateOperatorCredential(credential string) error {
	size := len([]byte(credential))
	if size < operatorMinCredentialBytes || size > operatorMaxCredentialBytes {
		return validationField("credential", "must be between 32 and 1024 bytes")
	}
	return nil
}
func operatorPrincipal(owner port.ExternalOwner) Principal {
	return Principal{CredentialID: "operator:" + owner.Key + "\x00" + owner.UID}
}
func operatorDatabaseNow(ctx context.Context, repositories Repositories) (time.Time, error) {
	if repositories.Runs == nil {
		return time.Time{}, errors.New("operator clock repository is not configured")
	}
	now, err := repositories.Runs.DatabaseNow(ctx)
	return now.UTC(), err
}
func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func requireOperatorMonitorRepositories(repositories Repositories) error {
	if repositories.Operator == nil || repositories.Locations == nil || repositories.Monitors == nil || repositories.Health == nil || repositories.Management == nil || repositories.ManagementCommands == nil || repositories.Runs == nil || repositories.Idempotency == nil {
		return errors.New("operator monitor repositories are not configured")
	}
	return nil
}
func requireOperatorAgentRepositories(repositories Repositories) error {
	if repositories.Operator == nil || repositories.Locations == nil || repositories.Agents == nil || repositories.Management == nil || repositories.ManagementCommands == nil || repositories.Runs == nil || repositories.Idempotency == nil {
		return errors.New("operator agent repositories are not configured")
	}
	return nil
}

func operatorMonitorState(ctx context.Context, repositories Repositories, id domain.MonitorID) (OperatorMonitorState, error) {
	health, err := repositories.Health.GetMonitor(ctx, id)
	if err != nil {
		return OperatorMonitorState{}, err
	}
	locations, err := repositories.Health.ListRequiredLocations(ctx, id)
	if err != nil {
		return OperatorMonitorState{}, err
	}
	var observed time.Time
	for _, location := range locations {
		if location.LastObservedAt.After(observed) {
			observed = location.LastObservedAt
		}
	}
	return OperatorMonitorState{ExternalID: id, State: health.State, LastTransitionAt: health.LastTransitionAt, LastObservedAt: observed}, nil
}
func operatorAgentState(ctx context.Context, repositories Repositories, id domain.AgentID) (OperatorAgentState, error) {
	agent, err := repositories.Agents.Get(ctx, id)
	if err != nil {
		return OperatorAgentState{}, err
	}
	state := OperatorAgentState{ExternalID: id, CredentialGeneration: agent.Agent.CredentialGeneration, LastSeenAt: agent.Agent.LastSeenAt}
	if agent.LastCompleteDiscoveryAt != nil {
		state.LastDiscoveryAt = *agent.LastCompleteDiscoveryAt
	}
	return state, nil
}
