package application

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/araihu/xisnove/domain"
)

var ErrInvalidEnrollmentToken = errors.New("invalid enrollment token")

// AgentService's existing TokenIssuer is also the explicit hasher supplied to
// OperatorService when registering caller-owned Agent credentials.
var _ CredentialHasher = (TokenIssuer)(nil)

type EnrollmentCredential struct {
	Token     string
	ExpiresAt time.Time
}

type EnrollAgentCommand struct {
	Token          string
	Name           string
	Capabilities   []domain.AgentCapability
	Credential     string
	IdempotencyKey string
}

type EnrolledAgentCredential struct {
	domain.Agent
	Credential string
}

type AgentServiceConfig struct {
	Store  UnitOfWork
	Tokens TokenIssuer
	Now    func() time.Time
	NewID  func() string
}

type AgentService struct {
	store  UnitOfWork
	tokens TokenIssuer
	now    func() time.Time
	newID  func() string
}

func NewAgentService(config AgentServiceConfig) *AgentService {
	return &AgentService{
		store: config.Store, tokens: config.Tokens, now: config.Now, newID: config.NewID,
	}
}

func (s *AgentService) CreateEnrollmentToken(
	ctx context.Context,
	locationID domain.LocationID,
	ttl time.Duration,
) (EnrollmentCredential, error) {
	if err := s.store.View(ctx, func(ctx context.Context, repositories Repositories) error {
		_, err := repositories.Locations.Get(ctx, locationID)
		return err
	}); err != nil {
		return EnrollmentCredential{}, err
	}
	ttl = enrollmentTTL(ttl)
	token, err := s.tokens.New()
	if err != nil {
		return EnrollmentCredential{}, fmt.Errorf("issue enrollment token: %w", err)
	}
	now := s.now().UTC()
	expiresAt := now.Add(ttl)
	err = s.store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		return repositories.Agents.CreateEnrollmentToken(ctx, EnrollmentTokenRecord{
			ID:         s.newID(),
			LocationID: locationID,
			TokenHash:  token.Hash,
			ExpiresAt:  expiresAt,
			CreatedAt:  now,
		})
	})
	if err != nil {
		return EnrollmentCredential{}, fmt.Errorf("create enrollment token: %w", err)
	}
	return EnrollmentCredential{Token: token.Raw, ExpiresAt: expiresAt}, nil
}

func (s *AgentService) Enroll(
	ctx context.Context,
	command EnrollAgentCommand,
) (EnrolledAgentCredential, error) {
	if command.Token == "" {
		return EnrolledAgentCredential{}, ErrInvalidEnrollmentToken
	}
	if command.Credential != "" || command.IdempotencyKey != "" {
		return s.enrollCallerCredential(ctx, command)
	}
	credential, err := s.tokens.New()
	if err != nil {
		return EnrolledAgentCredential{}, fmt.Errorf("issue agent credential: %w", err)
	}
	now := s.now().UTC()
	var enrolled domain.Agent
	err = s.store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		enrollment, consumed, err := repositories.Agents.ConsumeEnrollmentToken(
			ctx,
			s.tokens.Hash(command.Token),
			now,
			now,
		)
		if err != nil {
			return fmt.Errorf("consume enrollment token: %w", err)
		}
		if !consumed {
			return ErrInvalidEnrollmentToken
		}

		enrolled, err = domain.NewAgent(domain.NewAgentParams{
			ID:                   domain.AgentID(s.newID()),
			LocationID:           enrollment.LocationID,
			Name:                 command.Name,
			Capabilities:         command.Capabilities,
			CredentialGeneration: 1,
			CreatedAt:            now,
		})
		if err != nil {
			return &ValidationError{
				Fields: map[string]string{"agent": "contains invalid configuration"},
			}
		}
		if err := repositories.Agents.Create(ctx, AgentRecord{
			Agent: enrolled, CredentialHash: credential.Hash,
		}); err != nil {
			return fmt.Errorf("create agent: %w", err)
		}
		return nil
	})
	if err != nil {
		return EnrolledAgentCredential{}, err
	}
	return EnrolledAgentCredential{Agent: enrolled, Credential: credential.Raw}, nil
}

func (s *AgentService) enrollCallerCredential(
	ctx context.Context,
	command EnrollAgentCommand,
) (EnrolledAgentCredential, error) {
	fields := make(map[string]string)
	if len(command.Credential) < 32 {
		fields["credential"] = "must contain at least 32 characters"
	}
	if command.IdempotencyKey == "" {
		fields["idempotencyKey"] = "is required"
	}
	if len(fields) != 0 {
		return EnrolledAgentCredential{}, &ValidationError{Fields: fields}
	}

	tokenHash := s.tokens.Hash(command.Token)
	credentialHash := s.tokens.Hash(command.Credential)
	plannedID := domain.AgentID(s.newID())
	now := s.now().UTC()
	type enrollmentRequest struct {
		TokenHash      string                   `json:"tokenHash"`
		CredentialHash string                   `json:"credentialHash"`
		Name           string                   `json:"name"`
		Capabilities   []domain.AgentCapability `json:"capabilities"`
	}
	request := enrollmentRequest{
		TokenHash: hex.EncodeToString(tokenHash), CredentialHash: hex.EncodeToString(credentialHash),
		Name: command.Name, Capabilities: append([]domain.AgentCapability(nil), command.Capabilities...),
	}
	service := NewIdempotencyService[EnrolledAgentCredential](s.store)
	return service.Execute(ctx, IdempotencyRequest{
		Principal:   Principal{CredentialID: "enrollment:" + request.TokenHash},
		OperationID: "enrollAgent", Key: command.IdempotencyKey, Request: request,
		ResourceKind: "agent",
	}, func(ctx context.Context, repositories Repositories) (string, EnrolledAgentCredential, error) {
		enrollment, consumed, err := repositories.Agents.ConsumeEnrollmentToken(ctx, tokenHash, now, now)
		if err != nil {
			return "", EnrolledAgentCredential{}, fmt.Errorf("consume enrollment token: %w", err)
		}
		if !consumed {
			return "", EnrolledAgentCredential{}, ErrInvalidEnrollmentToken
		}
		enrolled, err := domain.NewAgent(domain.NewAgentParams{
			ID: plannedID, LocationID: enrollment.LocationID, Name: command.Name,
			Capabilities: command.Capabilities, CredentialGeneration: 1, CreatedAt: now,
		})
		if err != nil {
			return "", EnrolledAgentCredential{}, &ValidationError{Fields: map[string]string{"agent": "contains invalid configuration"}}
		}
		if err := repositories.Agents.Create(ctx, AgentRecord{Agent: enrolled, CredentialHash: credentialHash}); err != nil {
			return "", EnrolledAgentCredential{}, fmt.Errorf("create agent: %w", err)
		}
		return string(enrolled.ID), EnrolledAgentCredential{Agent: enrolled, Credential: command.Credential}, nil
	}, func(ctx context.Context, repositories Repositories, resourceID string) (EnrolledAgentCredential, error) {
		record, err := repositories.Agents.Get(ctx, domain.AgentID(resourceID))
		if err != nil {
			return EnrolledAgentCredential{}, err
		}
		if subtle.ConstantTimeCompare(record.CredentialHash, credentialHash) != 1 {
			return EnrolledAgentCredential{}, ErrIdempotencyKeyReused
		}
		return EnrolledAgentCredential{Agent: record.Agent, Credential: command.Credential}, nil
	})
}

func (s *AgentService) Authenticate(
	ctx context.Context,
	rawCredential string,
) (Principal, error) {
	if rawCredential == "" {
		return Principal{}, ErrInvalidCredentials
	}
	var record AgentRecord
	err := s.store.View(ctx, func(ctx context.Context, repositories Repositories) error {
		var findErr error
		record, findErr = repositories.Agents.FindActiveByCredentialHash(
			ctx,
			s.tokens.Hash(rawCredential),
		)
		return findErr
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Principal{}, ErrInvalidCredentials
		}
		return Principal{}, fmt.Errorf("authenticate agent: %w", err)
	}
	return Principal{
		Kind:                 PrincipalAgent,
		SubjectID:            string(record.Agent.ID),
		CredentialKind:       CredentialAgent,
		CredentialID:         string(record.Agent.ID),
		CredentialGeneration: record.PresentedCredentialGeneration,
	}, nil
}

func (s *AgentService) Heartbeat(
	ctx context.Context,
	principal Principal,
	credentialGeneration uint64,
	version string,
	capabilities []domain.AgentCapability,
) error {
	if principal.Kind != PrincipalAgent ||
		principal.SubjectID == "" ||
		principal.CredentialGeneration == 0 ||
		credentialGeneration == 0 ||
		credentialGeneration != principal.CredentialGeneration {
		return ErrInvalidCredentials
	}
	version = strings.TrimSpace(version)
	if version == "" || domain.ValidateAgentCapabilities(capabilities) != nil {
		return &ValidationError{
			Fields: map[string]string{"heartbeat": "contains invalid agent metadata"},
		}
	}
	var updated bool
	err := s.store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		var updateErr error
		updated, updateErr = repositories.Agents.UpdateHeartbeat(
			ctx,
			domain.AgentID(principal.SubjectID),
			credentialGeneration,
			version,
			capabilities,
			s.now().UTC(),
		)
		return updateErr
	})
	if err != nil {
		return fmt.Errorf("update agent heartbeat: %w", err)
	}
	if !updated {
		return ErrInvalidCredentials
	}
	return nil
}

func enrollmentTTL(ttl time.Duration) time.Duration {
	if ttl == 0 {
		return 15 * time.Minute
	}
	if ttl < time.Minute {
		return time.Minute
	}
	if ttl > time.Hour {
		return time.Hour
	}
	return ttl
}
