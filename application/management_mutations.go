package application

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

type UpdateLocationCommand struct {
	Name    *string
	Enabled *bool
}

type ReplaceMonitorCommand struct {
	CreateMonitorCommand
	Enabled bool
}

type UpdateAgentCommand struct {
	Name         *string
	Enabled      *bool
	Capabilities *[]domain.AgentCapability
}

type AgentCredential struct {
	AgentID              domain.AgentID
	Credential           string
	CredentialGeneration uint64
}

func (s *ManagementService) UpdateLocation(
	ctx context.Context,
	principal Principal,
	id domain.LocationID,
	idempotencyKey string,
	command UpdateLocationCommand,
) (domain.Location, error) {
	if err := authorizeManagementMutation("updateLocation", principal); err != nil {
		return domain.Location{}, err
	}
	if command.Name == nil && command.Enabled == nil {
		return domain.Location{}, validationField("location", "must include at least one change")
	}
	service := NewIdempotencyService[domain.Location](s.store)
	return service.Execute(ctx, IdempotencyRequest{
		Principal: principal, OperationID: "updateLocation", Key: idempotencyKey,
		Request: struct {
			ID      domain.LocationID
			Command UpdateLocationCommand
		}{id, command}, ResourceKind: "location",
	}, func(ctx context.Context, repositories Repositories) (string, domain.Location, error) {
		if err := requireManagementMutationRepositories(repositories); err != nil {
			return "", domain.Location{}, err
		}
		current, err := repositories.Management.GetLocation(ctx, id)
		if err != nil {
			return "", domain.Location{}, err
		}
		updated := current
		changed := make([]string, 0, 2)
		if command.Name != nil {
			updated.Name = strings.TrimSpace(*command.Name)
			if updated.Name == "" {
				return "", domain.Location{}, validationField("name", "must not be empty")
			}
			if updated.Name != current.Name {
				changed = append(changed, "name")
			}
		}
		if command.Enabled != nil {
			updated.Enabled = *command.Enabled
			if updated.Enabled != current.Enabled {
				changed = append(changed, "enabled")
			}
		}
		if len(changed) == 0 {
			return string(id), current, nil
		}
		now, err := managementDatabaseNow(ctx, repositories, "location update")
		if err != nil {
			return "", domain.Location{}, err
		}
		updated.UpdatedAt = now
		replaced, err := repositories.ManagementCommands.ReplaceLocation(ctx, updated)
		if err != nil {
			return "", domain.Location{}, fmt.Errorf("update location: %w", err)
		}
		if !replaced {
			return "", domain.Location{}, ErrConflict
		}
		if err := s.appendManagementAudit(ctx, repositories, "location.updated", "location", string(id), changed, now); err != nil {
			return "", domain.Location{}, err
		}
		if current.Enabled != updated.Enabled {
			reason := domain.StateTickReasonLocationPaused
			lifecycle := domain.MonitorLifecycleDisabled
			if updated.Enabled {
				reason = domain.StateTickReasonResumedByUser
				lifecycle = domain.MonitorLifecycleActive
			}
			if err := appendLocationAdministrativeStateTicks(
				ctx, repositories, updated.ID, lifecycle, reason,
				domain.StateTickActor{Kind: domain.StateTickActorUser, ID: principal.SubjectID},
				stateTickUserActionID(s.newID), now, s.newID,
			); err != nil {
				return "", domain.Location{}, err
			}
		}
		return string(id), updated, nil
	}, func(ctx context.Context, repositories Repositories, resourceID string) (domain.Location, error) {
		return repositories.Management.GetLocation(ctx, domain.LocationID(resourceID))
	})
}

func (s *ManagementService) DisableLocation(ctx context.Context, principal Principal, id domain.LocationID) error {
	if err := authorizeManagementMutation("disableLocation", principal); err != nil {
		return err
	}
	if err := s.mutationReady(); err != nil {
		return err
	}
	return s.store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		if err := requireManagementMutationRepositories(repositories); err != nil {
			return err
		}
		current, err := repositories.Management.GetLocation(ctx, id)
		if err != nil {
			return err
		}
		if !current.Enabled {
			return nil
		}
		now, err := managementDatabaseNow(ctx, repositories, "location disable")
		if err != nil {
			return err
		}
		disabled, err := repositories.ManagementCommands.DisableLocation(ctx, id, now)
		if err != nil {
			return fmt.Errorf("disable location: %w", err)
		}
		if !disabled {
			return ErrConflict
		}
		if err := s.appendManagementAudit(ctx, repositories, "location.disabled", "location", string(id), []string{"enabled"}, now); err != nil {
			return err
		}
		return appendLocationAdministrativeStateTicks(
			ctx, repositories, id, domain.MonitorLifecycleDisabled,
			domain.StateTickReasonLocationPaused,
			domain.StateTickActor{Kind: domain.StateTickActorUser, ID: principal.SubjectID},
			stateTickUserActionID(s.newID), now, s.newID,
		)
	})
}

func (s *ManagementService) UpdateMonitor(
	ctx context.Context,
	principal Principal,
	id domain.MonitorID,
	idempotencyKey string,
	command ReplaceMonitorCommand,
) (ConfiguredMonitor, error) {
	if err := authorizeManagementMutation("updateMonitor", principal); err != nil {
		return ConfiguredMonitor{}, err
	}
	service := NewIdempotencyService[ConfiguredMonitor](s.store)
	return service.Execute(ctx, IdempotencyRequest{
		Principal: principal, OperationID: "updateMonitor", Key: idempotencyKey,
		Request: struct {
			ID      domain.MonitorID
			Command ReplaceMonitorCommand
		}{id, command}, ResourceKind: "monitor",
	}, func(ctx context.Context, repositories Repositories) (string, ConfiguredMonitor, error) {
		if err := requireManagementMutationRepositories(repositories); err != nil {
			return "", ConfiguredMonitor{}, err
		}
		current, err := repositories.Management.GetMonitor(ctx, id)
		if err != nil {
			return "", ConfiguredMonitor{}, err
		}
		monitor, err := newConfiguredMonitor(id, command.CreateMonitorCommand, current.Monitor.CreatedAt)
		if err != nil {
			return "", ConfiguredMonitor{}, validationField("monitor", "contains invalid configuration")
		}
		if _, err := repositories.Management.GetLocation(ctx, command.LocationID); err != nil {
			return "", ConfiguredMonitor{}, err
		}
		monitor.Enabled = command.Enabled
		monitor.NextRunAt = current.Monitor.NextRunAt
		candidate := port.MonitorRecord{
			Monitor: monitor, LocationID: command.LocationID, RequiredLocation: command.RequiredLocation,
		}
		if monitorRecordsEqual(current, candidate) {
			return string(id), configuredMonitor(current), nil
		}
		now, err := managementDatabaseNow(ctx, repositories, "monitor replacement")
		if err != nil {
			return "", ConfiguredMonitor{}, err
		}
		candidate.Monitor.UpdatedAt = now
		replaced, err := repositories.ManagementCommands.ReplaceMonitor(ctx, candidate)
		if err != nil {
			return "", ConfiguredMonitor{}, fmt.Errorf("replace monitor: %w", err)
		}
		if !replaced {
			return "", ConfiguredMonitor{}, ErrConflict
		}
		if err := s.appendManagementAudit(ctx, repositories, "monitor.updated", "monitor", string(id), []string{"configuration"}, now); err != nil {
			return "", ConfiguredMonitor{}, err
		}
		if current.Monitor.Enabled != candidate.Monitor.Enabled {
			locationID := candidate.LocationID
			reason := domain.StateTickReasonMonitorPaused
			lifecycle := domain.MonitorLifecycleDisabled
			if candidate.Monitor.Enabled {
				reason = domain.StateTickReasonResumedByUser
				lifecycle = domain.MonitorLifecycleActive
			}
			if err := appendAdministrativeStateTick(
				ctx, repositories, candidate.Monitor, &locationID, lifecycle, reason,
				domain.StateTickActor{Kind: domain.StateTickActorUser, ID: principal.SubjectID},
				stateTickUserActionID(s.newID), now, s.newID,
			); err != nil {
				return "", ConfiguredMonitor{}, err
			}
		}
		return string(id), configuredMonitor(candidate), nil
	}, func(ctx context.Context, repositories Repositories, resourceID string) (ConfiguredMonitor, error) {
		record, err := repositories.Management.GetMonitor(ctx, domain.MonitorID(resourceID))
		return configuredMonitor(record), err
	})
}

func (s *ManagementService) DisableMonitor(ctx context.Context, principal Principal, id domain.MonitorID) error {
	if err := authorizeManagementMutation("disableMonitor", principal); err != nil {
		return err
	}
	if err := s.mutationReady(); err != nil {
		return err
	}
	return s.store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		if err := requireManagementMutationRepositories(repositories); err != nil {
			return err
		}
		current, err := repositories.Management.GetMonitor(ctx, id)
		if err != nil {
			return err
		}
		if !current.Monitor.Enabled {
			return nil
		}
		now, err := managementDatabaseNow(ctx, repositories, "monitor disable")
		if err != nil {
			return err
		}
		disabled, err := repositories.ManagementCommands.DisableMonitor(ctx, id, now)
		if err != nil {
			return fmt.Errorf("disable monitor: %w", err)
		}
		if !disabled {
			return ErrConflict
		}
		if err := s.appendManagementAudit(ctx, repositories, "monitor.disabled", "monitor", string(id), []string{"enabled"}, now); err != nil {
			return err
		}
		locationID := current.LocationID
		return appendAdministrativeStateTick(
			ctx, repositories, current.Monitor, &locationID,
			domain.MonitorLifecycleDisabled, domain.StateTickReasonMonitorPaused,
			domain.StateTickActor{Kind: domain.StateTickActorUser, ID: principal.SubjectID},
			stateTickUserActionID(s.newID), now, s.newID,
		)
	})
}

func (s *ManagementService) UpdateAgent(
	ctx context.Context,
	principal Principal,
	id domain.AgentID,
	idempotencyKey string,
	command UpdateAgentCommand,
) (domain.Agent, error) {
	if err := authorizeManagementMutation("updateAgent", principal); err != nil {
		return domain.Agent{}, err
	}
	if command.Name == nil && command.Enabled == nil && command.Capabilities == nil {
		return domain.Agent{}, validationField("agent", "must include at least one change")
	}
	service := NewIdempotencyService[domain.Agent](s.store)
	return service.Execute(ctx, IdempotencyRequest{
		Principal: principal, OperationID: "updateAgent", Key: idempotencyKey,
		Request: struct {
			ID      domain.AgentID
			Command UpdateAgentCommand
		}{id, command}, ResourceKind: "agent",
	}, func(ctx context.Context, repositories Repositories) (string, domain.Agent, error) {
		if err := requireManagementMutationRepositories(repositories); err != nil {
			return "", domain.Agent{}, err
		}
		current, err := repositories.Management.GetAgent(ctx, id)
		if err != nil {
			return "", domain.Agent{}, err
		}
		if command.Enabled != nil && *command.Enabled && current.RevokedAt != nil {
			return "", domain.Agent{}, ErrConflict
		}
		updated := cloneAgent(current)
		changed := make([]string, 0, 3)
		if command.Name != nil {
			updated.Name = strings.TrimSpace(*command.Name)
			if updated.Name != current.Name {
				changed = append(changed, "name")
			}
		}
		if command.Capabilities != nil {
			updated.Capabilities = slices.Clone(*command.Capabilities)
			if !slices.Equal(updated.Capabilities, current.Capabilities) {
				changed = append(changed, "capabilities")
			}
		}
		if _, err := domain.NewAgent(domain.NewAgentParams{
			ID: updated.ID, LocationID: updated.LocationID, Name: updated.Name,
			Capabilities: updated.Capabilities, CredentialGeneration: updated.CredentialGeneration,
			CreatedAt: updated.CreatedAt,
		}); err != nil {
			return "", domain.Agent{}, validationField("agent", "contains invalid configuration")
		}
		mustRevoke := command.Enabled != nil && !*command.Enabled && current.RevokedAt == nil
		if mustRevoke {
			changed = append(changed, "enabled")
		}
		if len(changed) == 0 {
			return string(id), current, nil
		}
		now, err := managementDatabaseNow(ctx, repositories, "agent update")
		if err != nil {
			return "", domain.Agent{}, err
		}
		updated.UpdatedAt = now
		metadataChanged := !slices.Equal(updated.Capabilities, current.Capabilities) || updated.Name != current.Name
		if metadataChanged {
			replaced, err := repositories.ManagementCommands.UpdateAgent(ctx, updated)
			if err != nil {
				return "", domain.Agent{}, fmt.Errorf("update agent: %w", err)
			}
			if !replaced {
				return "", domain.Agent{}, ErrConflict
			}
		}
		kind := "agent.updated"
		if mustRevoke {
			revoked, err := repositories.ManagementCommands.RevokeAgent(ctx, id, now)
			if err != nil {
				return "", domain.Agent{}, fmt.Errorf("revoke agent: %w", err)
			}
			if !revoked {
				return "", domain.Agent{}, ErrConflict
			}
			updated.RevokedAt = cloneTime(&now)
			kind = "agent.revoked"
		}
		if err := s.appendManagementAudit(ctx, repositories, kind, "agent", string(id), changed, now); err != nil {
			return "", domain.Agent{}, err
		}
		return string(id), updated, nil
	}, func(ctx context.Context, repositories Repositories, resourceID string) (domain.Agent, error) {
		agent, err := repositories.Management.GetAgent(ctx, domain.AgentID(resourceID))
		return cloneAgent(agent), err
	})
}

func (s *ManagementService) RevokeAgent(ctx context.Context, principal Principal, id domain.AgentID) error {
	if err := authorizeManagementMutation("revokeAgent", principal); err != nil {
		return err
	}
	if err := s.mutationReady(); err != nil {
		return err
	}
	return s.store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		if err := requireManagementMutationRepositories(repositories); err != nil {
			return err
		}
		current, err := repositories.Management.GetAgent(ctx, id)
		if err != nil {
			return err
		}
		if current.RevokedAt != nil {
			return nil
		}
		now, err := managementDatabaseNow(ctx, repositories, "agent revocation")
		if err != nil {
			return err
		}
		revoked, err := repositories.ManagementCommands.RevokeAgent(ctx, id, now)
		if err != nil {
			return fmt.Errorf("revoke agent: %w", err)
		}
		if !revoked {
			return ErrConflict
		}
		return s.appendManagementAudit(ctx, repositories, "agent.revoked", "agent", string(id), []string{"enabled"}, now)
	})
}

func (s *ManagementService) RotateAgentCredential(
	ctx context.Context,
	principal Principal,
	id domain.AgentID,
	idempotencyKey string,
) (AgentCredential, error) {
	if err := authorizeManagementMutation("rotateAgentCredential", principal); err != nil {
		return AgentCredential{}, err
	}
	if s == nil || s.tokens == nil {
		return AgentCredential{}, errors.New("management credential issuer is not configured")
	}
	service := NewIdempotencyService[AgentCredential](s.store)
	return service.Execute(ctx, IdempotencyRequest{
		Principal: principal, OperationID: "rotateAgentCredential", Key: idempotencyKey,
		Request: struct{ AgentID domain.AgentID }{id}, ResourceKind: "agent-credential",
		ResourceID: string(id), CredentialIssuance: true,
	}, func(ctx context.Context, repositories Repositories) (string, AgentCredential, error) {
		if err := requireManagementMutationRepositories(repositories); err != nil {
			return "", AgentCredential{}, err
		}
		agent, err := repositories.Management.GetAgent(ctx, id)
		if err != nil {
			return "", AgentCredential{}, err
		}
		if agent.RevokedAt != nil {
			return "", AgentCredential{}, ErrConflict
		}
		issued, err := s.tokens.New()
		if err != nil {
			return "", AgentCredential{}, fmt.Errorf("issue agent credential: %w", err)
		}
		if err := validateIssuedToken(s.tokens, issued); err != nil {
			return "", AgentCredential{}, err
		}
		now, err := managementDatabaseNow(ctx, repositories, "agent credential rotation")
		if err != nil {
			return "", AgentCredential{}, err
		}
		generation := agent.CredentialGeneration + 1
		created, err := repositories.ManagementCommands.CreateAgentCredentialGeneration(ctx, port.CreateAgentCredentialGenerationCommand{
			ExpectedCurrentGeneration: agent.CredentialGeneration,
			Credential: port.AgentCredentialRecord{
				AgentID: id, Generation: generation, CredentialHash: slices.Clone(issued.Hash), CreatedAt: now,
			},
		})
		if err != nil {
			return "", AgentCredential{}, fmt.Errorf("rotate agent credential: %w", err)
		}
		if !created {
			return "", AgentCredential{}, ErrConflict
		}
		if err := s.appendManagementAudit(ctx, repositories, "agent.credential-rotated", "agent", string(id), []string{"credentialGeneration"}, now); err != nil {
			return "", AgentCredential{}, err
		}
		return string(id), AgentCredential{
			AgentID: id, Credential: issued.Raw, CredentialGeneration: generation,
		}, nil
	}, nil)
}

func (s *ManagementService) RevokeAgentCredentialGeneration(
	ctx context.Context,
	principal Principal,
	id domain.AgentID,
	generation uint64,
) error {
	if err := authorizeManagementMutation("revokeAgentCredentialGeneration", principal); err != nil {
		return err
	}
	if generation == 0 {
		return validationField("generation", "must be at least one")
	}
	if err := s.mutationReady(); err != nil {
		return err
	}
	return s.store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		if err := requireManagementMutationRepositories(repositories); err != nil {
			return err
		}
		now, err := managementDatabaseNow(ctx, repositories, "agent credential revocation")
		if err != nil {
			return err
		}
		outcome, err := repositories.ManagementCommands.RevokeAgentCredentialGeneration(ctx, id, generation, now)
		if err != nil {
			return fmt.Errorf("revoke agent credential generation: %w", err)
		}
		switch outcome {
		case port.CredentialGenerationAlreadyRevoked:
			return nil
		case port.CredentialGenerationNotFound:
			return ErrNotFound
		case port.CredentialGenerationCurrent, port.CredentialGenerationReplacementUnobserved:
			return ErrConflict
		case port.CredentialGenerationRevoked:
			return s.appendManagementAudit(ctx, repositories, "agent.credential-generation-revoked", "agent", string(id), []string{"credentialGeneration"}, now)
		default:
			return fmt.Errorf("revoke agent credential generation: unknown outcome %q", outcome)
		}
	})
}

func authorizeManagementMutation(operationID string, principal Principal) error {
	if err := Authorize(operationID, principal); err != nil {
		return err
	}
	if principal.SubjectID == "" || principal.CredentialID == "" {
		return ErrInvalidCredentials
	}
	switch principal.Kind {
	case PrincipalAdmin:
		if principal.CredentialKind != CredentialSession {
			return ErrInvalidCredentials
		}
	case PrincipalAPIToken:
		if principal.CredentialKind != CredentialAPIToken {
			return ErrInvalidCredentials
		}
	default:
		return ErrForbidden
	}
	return nil
}

func requireManagementMutationRepositories(repositories Repositories) error {
	if repositories.Management == nil || repositories.ManagementCommands == nil ||
		repositories.Runs == nil || repositories.Audit == nil {
		return errors.New("management mutation repositories are not configured")
	}
	return nil
}

func (s *ManagementService) mutationReady() error {
	if s == nil || s.store == nil {
		return errors.New("management mutation service is not configured")
	}
	return nil
}

func managementDatabaseNow(ctx context.Context, repositories Repositories, operation string) (time.Time, error) {
	now, err := repositories.Runs.DatabaseNow(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("read database time for %s: %w", operation, err)
	}
	return now.UTC(), nil
}

func (s *ManagementService) appendManagementAudit(
	ctx context.Context,
	repositories Repositories,
	kind string,
	subjectKind string,
	subjectID string,
	fields []string,
	createdAt time.Time,
) error {
	if s == nil || s.newID == nil {
		return errors.New("management audit ID generator is not configured")
	}
	payload, err := json.Marshal(struct {
		Fields []string `json:"fields"`
	}{Fields: slices.Clone(fields)})
	if err != nil {
		return fmt.Errorf("encode management audit: %w", err)
	}
	if err := repositories.Audit.Append(ctx, port.AuditEventRecord{
		ID: s.newID(), Kind: kind, SubjectKind: subjectKind, SubjectID: subjectID,
		Payload: payload, CreatedAt: createdAt,
	}); err != nil {
		return fmt.Errorf("append management audit: %w", err)
	}
	return nil
}

func validateIssuedToken(issuer TokenIssuer, issued IssuedToken) error {
	computed := issuer.Hash(issued.Raw)
	if issued.Raw == "" || len(issued.Hash) == 0 || len(computed) != len(issued.Hash) ||
		subtle.ConstantTimeCompare(computed, issued.Hash) != 1 {
		return errors.New("token issuer returned inconsistent credential")
	}
	return nil
}

func validationField(field, message string) error {
	return &ValidationError{Fields: map[string]string{field: message}}
}

func monitorRecordsEqual(left, right port.MonitorRecord) bool {
	left.Monitor.UpdatedAt = time.Time{}
	right.Monitor.UpdatedAt = time.Time{}
	return reflect.DeepEqual(left, right)
}
