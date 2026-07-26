package application

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

var ErrDiscoveryBatchTooLarge = errors.New("discovery batch exceeds 500 entries")

const MaxDiscoveryBatchSize = 500

type DiscoveryInput struct {
	LocationID                             domain.LocationID
	SourceKind, SourceUID, Namespace, Name string
	Labels                                 map[string]string
	Protocol                               domain.MonitorKind
	Target, NetworkPerspective             string
	Present                                bool
	ObservedAt                             time.Time
}

type DiscoveryPromotionCommand struct {
	Name, Description                   string
	Labels                              map[string]string
	DisplayOrder                        int32
	Public                              bool
	LocationID                          domain.LocationID
	RequiredLocation                    bool
	Interval, Timeout                   time.Duration
	FailureThreshold, RecoveryThreshold uint16
}

type DiscoveryPromotion struct {
	Candidate domain.DiscoveryCandidate
	Monitor   ConfiguredMonitor
}

type DiscoveryServiceConfig struct {
	Store          port.DiscoveryUnitOfWork
	NewCandidateID func() string
	NewMonitorID   func() string
	Now            func() time.Time
}

type DiscoveryService struct {
	store          port.DiscoveryUnitOfWork
	newCandidateID func() string
	newMonitorID   func() string
	now            func() time.Time
}

func NewDiscoveryService(config DiscoveryServiceConfig) *DiscoveryService {
	return &DiscoveryService{store: config.Store, newCandidateID: config.NewCandidateID, newMonitorID: config.NewMonitorID, now: config.Now}
}

func (s *DiscoveryService) Publish(ctx context.Context, agentID domain.AgentID, batchID string, inputs []DiscoveryInput) (port.DiscoveryBatchAcknowledgement, error) {
	if s == nil || s.store == nil || s.newCandidateID == nil || s.now == nil {
		return port.DiscoveryBatchAcknowledgement{}, errors.New("discovery service is not configured")
	}
	batchID = strings.TrimSpace(batchID)
	if agentID == "" || batchID == "" || len(inputs) == 0 {
		return port.DiscoveryBatchAcknowledgement{}, &ValidationError{Fields: map[string]string{"batch": "agent, batch id, and candidates are required"}}
	}
	if len(inputs) > MaxDiscoveryBatchSize {
		return port.DiscoveryBatchAcknowledgement{}, ErrDiscoveryBatchTooLarge
	}
	requestHash, err := CanonicalRequestFingerprint(struct {
		AgentID domain.AgentID
		Inputs  []DiscoveryInput
	}{agentID, inputs})
	if err != nil {
		return port.DiscoveryBatchAcknowledgement{}, fmt.Errorf("fingerprint discovery batch: %w", err)
	}
	candidates := make([]domain.DiscoveryCandidate, len(inputs))
	for index, input := range inputs {
		candidate, err := domain.NewDiscoveryCandidate(domain.NewDiscoveryCandidateParams{
			ID: domain.DiscoveryCandidateID(s.newCandidateID()), AgentID: agentID, LocationID: input.LocationID,
			SourceKind: input.SourceKind, SourceUID: input.SourceUID, Namespace: input.Namespace, Name: input.Name,
			Labels: input.Labels, Protocol: input.Protocol, Target: input.Target, NetworkPerspective: input.NetworkPerspective,
			Present: input.Present, ObservedAt: input.ObservedAt,
		})
		if err != nil {
			return port.DiscoveryBatchAcknowledgement{}, &ValidationError{Fields: map[string]string{fmt.Sprintf("candidates[%d]", index): "contains invalid discovery identity"}}
		}
		candidates[index] = candidate
	}
	var acknowledgement port.DiscoveryBatchAcknowledgement
	err = s.store.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		if repositories.Discovery == nil {
			return errors.New("discovery repository is not configured")
		}
		var applyErr error
		acknowledgement, applyErr = repositories.Discovery.ApplyBatch(ctx, port.DiscoveryBatch{ID: batchID, AgentID: agentID, RequestHash: requestHash, Candidates: candidates, CreatedAt: s.now().UTC()})
		return applyErr
	})
	return acknowledgement, err
}

func (s *DiscoveryService) Promote(ctx context.Context, candidateID domain.DiscoveryCandidateID, command DiscoveryPromotionCommand) (DiscoveryPromotion, error) {
	if s == nil || s.store == nil || s.newMonitorID == nil || s.now == nil {
		return DiscoveryPromotion{}, errors.New("discovery service is not configured")
	}
	var result DiscoveryPromotion
	err := s.store.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		if repositories.Discovery == nil || repositories.Locations == nil || repositories.Monitors == nil || repositories.Health == nil {
			return errors.New("discovery promotion repositories are not configured")
		}
		candidate, err := repositories.Discovery.GetForUpdate(ctx, candidateID)
		if err != nil {
			return err
		}
		if candidate.PromotedMonitorID != nil {
			monitor, err := repositories.Monitors.Get(ctx, *candidate.PromotedMonitorID)
			if err != nil {
				return err
			}
			assignment, err := repositories.Monitors.GetAssignment(ctx, monitor.ID)
			if err != nil {
				return err
			}
			result = DiscoveryPromotion{Candidate: candidate.Clone(), Monitor: ConfiguredMonitor{Monitor: monitor, LocationID: assignment.LocationID, RequiredLocation: assignment.Required}}
			return nil
		}
		if !candidate.Present {
			return &ValidationError{Fields: map[string]string{"candidate": "stale candidate cannot be promoted"}}
		}
		if command.LocationID != candidate.LocationID {
			return &ValidationError{Fields: map[string]string{"locationId": "must match candidate location"}}
		}
		if _, err := repositories.Locations.Get(ctx, command.LocationID); err != nil {
			return err
		}
		probe, err := discoveryProbe(candidate)
		if err != nil {
			return &ValidationError{Fields: map[string]string{"candidate": "target cannot be promoted"}}
		}
		now := s.now().UTC()
		monitor, err := newConfiguredMonitor(domain.MonitorID(s.newMonitorID()), CreateMonitorCommand{
			Name: command.Name, Description: command.Description, Labels: command.Labels, DisplayOrder: command.DisplayOrder,
			Public: command.Public, LocationID: command.LocationID, RequiredLocation: command.RequiredLocation,
			Interval: command.Interval, Timeout: command.Timeout, FailureThreshold: command.FailureThreshold,
			RecoveryThreshold: command.RecoveryThreshold, Probe: probe,
		}, now)
		if err != nil {
			return &ValidationError{Fields: map[string]string{"monitor": "contains invalid configuration"}}
		}
		if err := repositories.Monitors.Create(ctx, monitor); err != nil {
			return err
		}
		assignment := port.MonitorLocation{MonitorID: monitor.ID, LocationID: command.LocationID, Required: command.RequiredLocation}
		if err := repositories.Monitors.AssignLocation(ctx, assignment); err != nil {
			return err
		}
		if err := repositories.Health.UpsertLocation(ctx, domain.LocationHealth{MonitorID: monitor.ID, LocationID: command.LocationID, State: domain.HealthPending, LastTransitionAt: now}); err != nil {
			return err
		}
		if err := repositories.Health.UpsertMonitor(ctx, domain.MonitorHealth{MonitorID: monitor.ID, State: domain.HealthPending, LastTransitionAt: now}); err != nil {
			return err
		}
		linked, err := repositories.Discovery.LinkPromotion(ctx, candidate.ID, monitor.ID, now)
		if err != nil {
			return err
		}
		if !linked {
			return port.ErrConflict
		}
		candidate.PromotedMonitorID = &monitor.ID
		candidate.UpdatedAt = now
		result = DiscoveryPromotion{Candidate: candidate.Clone(), Monitor: ConfiguredMonitor{Monitor: monitor, LocationID: command.LocationID, RequiredLocation: command.RequiredLocation}}
		return nil
	})
	if err != nil {
		return DiscoveryPromotion{}, fmt.Errorf("promote discovery candidate: %w", err)
	}
	return result, nil
}

func (s *DiscoveryService) Get(ctx context.Context, id domain.DiscoveryCandidateID) (domain.DiscoveryCandidate, error) {
	var candidate domain.DiscoveryCandidate
	err := s.store.DiscoveryView(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		var err error
		candidate, err = repositories.Discovery.Get(ctx, id)
		return err
	})
	return candidate.Clone(), err
}

func discoveryProbe(candidate domain.DiscoveryCandidate) (domain.ProbeDefinition, error) {
	switch candidate.Protocol {
	case domain.MonitorKindHTTP:
		return domain.ProbeDefinition{Kind: domain.MonitorKindHTTP, HTTP: domain.HTTPProbe{Method: http.MethodGet, URL: candidate.Target, Headers: map[string]string{}, ExpectedStatus: []domain.StatusRange{{Min: 200, Max: 399}}}}, nil
	case domain.MonitorKindTCP:
		host, portText, err := net.SplitHostPort(candidate.Target)
		if err != nil {
			return domain.ProbeDefinition{}, err
		}
		portNumber, err := strconv.ParseUint(portText, 10, 16)
		if err != nil {
			return domain.ProbeDefinition{}, err
		}
		return domain.ProbeDefinition{Kind: domain.MonitorKindTCP, TCP: domain.TCPProbe{Host: host, Port: uint16(portNumber)}}, nil
	case domain.MonitorKindDNS:
		return domain.ProbeDefinition{Kind: domain.MonitorKindDNS, DNS: domain.DNSProbe{Name: candidate.Target, RecordType: "A"}}, nil
	default:
		return domain.ProbeDefinition{}, domain.ErrInvalidMonitor
	}
}
