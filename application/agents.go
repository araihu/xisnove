package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/araihu/xisnove/domain"
)

var ErrInvalidEnrollmentToken = errors.New("invalid enrollment token")

type EnrollmentCredential struct {
	Token     string
	ExpiresAt time.Time
}

type EnrollAgentCommand struct {
	Token        string
	Name         string
	Capabilities []domain.AgentCapability
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
