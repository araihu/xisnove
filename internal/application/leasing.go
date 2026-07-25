package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/araihu/xisnove/internal/domain"
)

var ErrNoWork = errors.New("no work available")

type ProbeWork struct {
	RunID        domain.CheckRunID
	MonitorID    domain.MonitorID
	ScheduledFor time.Time
	LeaseToken   string
	Timeout      time.Duration
	Probe        domain.ProbeDefinition
}

type LeaseServiceConfig struct {
	Store         Store
	Tokens        TokenIssuer
	LeaseDuration time.Duration
	PollInterval  time.Duration
}

type LeaseService struct {
	store         Store
	tokens        TokenIssuer
	leaseDuration time.Duration
	pollInterval  time.Duration
}

func NewLeaseService(config LeaseServiceConfig) *LeaseService {
	pollInterval := config.PollInterval
	if pollInterval <= 0 {
		pollInterval = 250 * time.Millisecond
	}
	return &LeaseService{
		store:         config.Store,
		tokens:        config.Tokens,
		leaseDuration: config.LeaseDuration,
		pollInterval:  pollInterval,
	}
}

func (s *LeaseService) LeaseProbe(
	ctx context.Context,
	agentID domain.AgentID,
	capabilities []domain.AgentCapability,
	wait time.Duration,
) (*ProbeWork, error) {
	agent, err := s.store.Repositories().Agents.Get(ctx, agentID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("load leasing agent: %w", err)
	}
	if agent.Agent.RevokedAt != nil ||
		len(capabilities) == 0 ||
		!capabilitiesSubset(capabilities, agent.Agent.Capabilities) {
		return nil, ErrInvalidCredentials
	}
	if wait < 0 {
		wait = 0
	}
	if wait > 30*time.Second {
		wait = 30 * time.Second
	}

	deadline := time.Now().Add(wait)
	for {
		work, err := s.tryLeaseProbe(ctx, agentID, capabilities)
		if err == nil {
			return work, nil
		}
		if !errors.Is(err, ErrNoWork) {
			return nil, err
		}
		remaining := time.Until(deadline)
		if wait == 0 || remaining <= 0 {
			return nil, ErrNoWork
		}
		delay := min(s.pollInterval, remaining)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *LeaseService) tryLeaseProbe(
	ctx context.Context,
	agentID domain.AgentID,
	capabilities []domain.AgentCapability,
) (*ProbeWork, error) {
	now, err := s.store.Repositories().Runs.DatabaseNow(ctx)
	if err != nil {
		return nil, fmt.Errorf("read database time: %w", err)
	}
	token, err := s.tokens.New()
	if err != nil {
		return nil, fmt.Errorf("issue lease token: %w", err)
	}
	run, err := s.store.Repositories().Runs.ClaimProbe(ctx, ClaimRunParams{
		AgentID:        agentID,
		Capabilities:   append([]domain.AgentCapability(nil), capabilities...),
		LeaseTokenHash: token.Hash,
		LeaseExpiresAt: now.Add(s.leaseDuration),
		Now:            now,
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNoWork
		}
		return nil, fmt.Errorf("claim probe run: %w", err)
	}
	return &ProbeWork{
		RunID:        run.ID,
		MonitorID:    run.MonitorID,
		ScheduledFor: run.ScheduledFor,
		LeaseToken:   token.Raw,
		Timeout:      run.Timeout,
		Probe:        run.Probe,
	}, nil
}

func capabilitiesSubset(
	requested []domain.AgentCapability,
	advertised []domain.AgentCapability,
) bool {
	for _, requestedCapability := range requested {
		found := false
		for _, advertisedCapability := range advertised {
			if requestedCapability == advertisedCapability {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
