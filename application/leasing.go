package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/araihu/xisnove/domain"
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
	Store         UnitOfWork
	Tokens        TokenIssuer
	LeaseDuration time.Duration
	PollInterval  time.Duration
	ObserveLease  func(LeaseObservation)
}

type LeaseService struct {
	store         UnitOfWork
	tokens        TokenIssuer
	leaseDuration time.Duration
	pollInterval  time.Duration
	observeLease  func(LeaseObservation)
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
		observeLease:  config.ObserveLease,
	}
}

func (s *LeaseService) LeaseProbe(
	ctx context.Context,
	agentID domain.AgentID,
	capabilities []domain.AgentCapability,
	wait time.Duration,
) (*ProbeWork, error) {
	var agent AgentRecord
	err := s.store.View(ctx, func(ctx context.Context, repositories Repositories) error {
		var getErr error
		agent, getErr = repositories.Agents.Get(ctx, agentID)
		return getErr
	})
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
		work, reclaimedExpired, err := s.tryLeaseProbe(ctx, agentID, capabilities)
		if err == nil {
			if s.observeLease != nil {
				outcome := LeaseClaimed
				if reclaimedExpired {
					outcome = LeaseExpired
				}
				s.observeLease(LeaseObservation{Outcome: outcome})
			}
			return work, nil
		}
		if !errors.Is(err, ErrNoWork) {
			return nil, err
		}
		remaining := time.Until(deadline)
		if wait == 0 || remaining <= 0 {
			if s.observeLease != nil {
				s.observeLease(LeaseObservation{Outcome: LeaseNoWork})
			}
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
) (*ProbeWork, bool, error) {
	token, err := s.tokens.New()
	if err != nil {
		return nil, false, fmt.Errorf("issue lease token: %w", err)
	}
	var run RunRecord
	err = s.store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		now, err := repositories.Runs.DatabaseNow(ctx)
		if err != nil {
			return fmt.Errorf("read database time: %w", err)
		}
		var claimErr error
		run, claimErr = repositories.Runs.ClaimProbe(ctx, ClaimRunParams{
			AgentID:        agentID,
			Capabilities:   append([]domain.AgentCapability(nil), capabilities...),
			LeaseTokenHash: token.Hash,
			LeaseExpiresAt: now.Add(s.leaseDuration),
			Now:            now,
		})
		return claimErr
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, false, ErrNoWork
		}
		return nil, false, fmt.Errorf("claim probe run: %w", err)
	}
	return &ProbeWork{
		RunID:        run.ID,
		MonitorID:    run.MonitorID,
		ScheduledFor: run.ScheduledFor,
		LeaseToken:   token.Raw,
		Timeout:      run.Timeout,
		Probe:        run.Probe,
	}, run.LeaseAttempt > 1, nil
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
