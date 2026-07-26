package application

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

var ErrDiscoveryBatchTooLarge = errors.New("discovery batch exceeds 500 entries")

const MaxDiscoveryBatchSize = 500

const discoveryCursorEndpoint = "/v1/discovery-candidates"

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
	Store            port.DiscoveryUnitOfWork
	IdempotencyStore port.UnitOfWork
	NewCandidateID   func() string
	NewMonitorID     func() string
	Now              func() time.Time
	Cursors          AudienceCursorCodec
}

type DiscoveryService struct {
	store            port.DiscoveryUnitOfWork
	idempotencyStore port.UnitOfWork
	newCandidateID   func() string
	newMonitorID     func() string
	now              func() time.Time
	cursors          AudienceCursorCodec
	promotionGatesMu sync.Mutex
	promotionGates   map[domain.DiscoveryCandidateID]*discoveryPromotionGate
}

type discoveryPromotionGate struct {
	semaphore chan struct{}
	users     int
}

func NewDiscoveryService(config DiscoveryServiceConfig) *DiscoveryService {
	return &DiscoveryService{
		store: config.Store, idempotencyStore: config.IdempotencyStore, newCandidateID: config.NewCandidateID,
		newMonitorID: config.NewMonitorID, now: config.Now, cursors: config.Cursors,
	}
}

func (s *DiscoveryService) Publish(ctx context.Context, agentID domain.AgentID, batchID string, inputs []DiscoveryInput) (port.DiscoveryBatchAcknowledgement, error) {
	return s.PublishSnapshot(ctx, agentID, batchID, false, time.Time{}, inputs)
}

// PublishSnapshot accepts either a delta (partial) publication or a complete
// point-in-time inventory. Only complete publications may be empty: an empty
// partial would carry no useful observation and must not become an accidental
// absence claim. A complete batch uses one observation time throughout so the
// repository can atomically fence and mark absent candidates.
func (s *DiscoveryService) PublishSnapshot(
	ctx context.Context,
	agentID domain.AgentID,
	batchID string,
	complete bool,
	completedAt time.Time,
	inputs []DiscoveryInput,
) (port.DiscoveryBatchAcknowledgement, error) {
	if s == nil || s.store == nil || s.newCandidateID == nil || s.now == nil {
		return port.DiscoveryBatchAcknowledgement{}, errors.New("discovery service is not configured")
	}
	batchID = strings.TrimSpace(batchID)
	if agentID == "" || batchID == "" {
		return port.DiscoveryBatchAcknowledgement{}, &ValidationError{Fields: map[string]string{"batch": "agent, batch id, and candidates are required"}}
	}
	if complete && completedAt.IsZero() {
		return port.DiscoveryBatchAcknowledgement{}, &ValidationError{Fields: map[string]string{"completedAt": "is required for a complete snapshot"}}
	}
	if !complete && len(inputs) == 0 {
		return port.DiscoveryBatchAcknowledgement{}, port.ErrConflict
	}
	if len(inputs) > MaxDiscoveryBatchSize {
		return port.DiscoveryBatchAcknowledgement{}, ErrDiscoveryBatchTooLarge
	}
	requestHash, err := CanonicalRequestFingerprint(struct {
		AgentID     domain.AgentID
		Complete    bool
		CompletedAt time.Time
		Inputs      []DiscoveryInput
	}{agentID, complete, completedAt.UTC(), inputs})
	if err != nil {
		return port.DiscoveryBatchAcknowledgement{}, fmt.Errorf("fingerprint discovery batch: %w", err)
	}
	candidates := make([]domain.DiscoveryCandidate, len(inputs))
	identities := make(map[domain.DiscoveryIdentity]int, len(inputs))
	for index, input := range inputs {
		if complete && !input.ObservedAt.Equal(completedAt) {
			return port.DiscoveryBatchAcknowledgement{}, &ValidationError{Fields: map[string]string{fmt.Sprintf("candidates[%d].observedAt", index): "must equal completedAt for a complete snapshot"}}
		}
		candidate, err := domain.NewDiscoveryCandidate(domain.NewDiscoveryCandidateParams{
			ID: domain.DiscoveryCandidateID(s.newCandidateID()), AgentID: agentID, LocationID: input.LocationID,
			SourceKind: input.SourceKind, SourceUID: input.SourceUID, Namespace: input.Namespace, Name: input.Name,
			Labels: input.Labels, Protocol: input.Protocol, Target: input.Target, NetworkPerspective: input.NetworkPerspective,
			Present: input.Present, ObservedAt: input.ObservedAt,
		})
		if err != nil {
			return port.DiscoveryBatchAcknowledgement{}, &ValidationError{Fields: map[string]string{fmt.Sprintf("candidates[%d]", index): "contains invalid discovery identity"}}
		}
		if previous, duplicate := identities[candidate.Identity()]; duplicate {
			return port.DiscoveryBatchAcknowledgement{}, &ValidationError{Fields: map[string]string{
				fmt.Sprintf("candidates[%d]", index): fmt.Sprintf("duplicates candidates[%d]", previous),
			}}
		}
		identities[candidate.Identity()] = index
		candidates[index] = candidate
	}
	var acknowledgement port.DiscoveryBatchAcknowledgement
	err = s.store.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		if repositories.Discovery == nil {
			return errors.New("discovery repository is not configured")
		}
		var applyErr error
		acknowledgement, applyErr = repositories.Discovery.ApplyBatch(ctx, port.DiscoveryBatch{ID: batchID, AgentID: agentID, RequestHash: requestHash, Candidates: candidates, Complete: complete, CompletedAt: completedAt.UTC(), CreatedAt: s.now().UTC()})
		return applyErr
	})
	return acknowledgement, err
}

func (s *DiscoveryService) Promote(ctx context.Context, candidateID domain.DiscoveryCandidateID, command DiscoveryPromotionCommand) (DiscoveryPromotion, error) {
	if s == nil || s.store == nil || s.newMonitorID == nil || s.now == nil {
		return DiscoveryPromotion{}, errors.New("discovery service is not configured")
	}
	release, err := s.acquirePromotionGate(ctx, candidateID)
	if err != nil {
		return DiscoveryPromotion{}, err
	}
	defer release()
	for attempt := 0; attempt < idempotencyMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return DiscoveryPromotion{}, err
		}
		var result DiscoveryPromotion
		err := s.store.DiscoveryTransact(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
			var err error
			result, err = s.promoteWithRepositories(ctx, repositories, candidateID, command)
			return err
		})
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, ErrRetryableTransaction) || attempt == idempotencyMaxAttempts-1 {
			return DiscoveryPromotion{}, fmt.Errorf("promote discovery candidate: %w", err)
		}
		if err := waitForIdempotencyRetry(ctx); err != nil {
			return DiscoveryPromotion{}, fmt.Errorf("promote discovery candidate: %w", err)
		}
	}
	return DiscoveryPromotion{}, errors.New("discovery promotion retry attempts exhausted")
}

func (s *DiscoveryService) PromoteIdempotently(
	ctx context.Context,
	principal Principal,
	idempotencyKey string,
	candidateID domain.DiscoveryCandidateID,
	command DiscoveryPromotionCommand,
) (DiscoveryPromotion, error) {
	if s == nil || s.idempotencyStore == nil || s.newMonitorID == nil || s.now == nil {
		return DiscoveryPromotion{}, errors.New("idempotent discovery promotion is not configured")
	}
	if err := authorizeManagementMutation("promoteDiscoveryCandidate", principal); err != nil {
		return DiscoveryPromotion{}, err
	}
	release, err := s.acquirePromotionGate(ctx, candidateID)
	if err != nil {
		return DiscoveryPromotion{}, err
	}
	defer release()
	service := NewIdempotencyService[DiscoveryPromotion](s.idempotencyStore)
	result, err := service.Execute(ctx, IdempotencyRequest{
		Principal: principal, OperationID: "promoteDiscoveryCandidate", Key: idempotencyKey,
		Request: struct {
			CandidateID domain.DiscoveryCandidateID
			Command     DiscoveryPromotionCommand
		}{candidateID, command},
		ResourceKind: "discovery-promotion",
	}, func(ctx context.Context, repositories Repositories) (string, DiscoveryPromotion, error) {
		promotion, err := s.promoteWithRepositories(ctx, discoveryRepositories(repositories), candidateID, command)
		return string(candidateID), promotion, err
	}, func(ctx context.Context, repositories Repositories, resourceID string) (DiscoveryPromotion, error) {
		return loadDiscoveryPromotion(ctx, discoveryRepositories(repositories), domain.DiscoveryCandidateID(resourceID))
	})
	if err != nil {
		return DiscoveryPromotion{}, fmt.Errorf("promote discovery candidate idempotently: %w", err)
	}
	return result, nil
}

// acquirePromotionGate coalesces same-process contention so optimistic SQLite
// transactions usually avoid allocating throwaway monitor IDs. It is not a
// distributed lock: durable candidate/link constraints and transaction retry
// remain the cross-replica correctness boundary, where an extra discarded UUID
// allocation is harmless.
func (s *DiscoveryService) acquirePromotionGate(ctx context.Context, candidateID domain.DiscoveryCandidateID) (func(), error) {
	s.promotionGatesMu.Lock()
	if s.promotionGates == nil {
		s.promotionGates = make(map[domain.DiscoveryCandidateID]*discoveryPromotionGate)
	}
	gate := s.promotionGates[candidateID]
	if gate == nil {
		gate = &discoveryPromotionGate{semaphore: make(chan struct{}, 1)}
		gate.semaphore <- struct{}{}
		s.promotionGates[candidateID] = gate
	}
	gate.users++
	s.promotionGatesMu.Unlock()

	select {
	case <-ctx.Done():
		s.releasePromotionGateUser(candidateID, gate)
		return nil, ctx.Err()
	case <-gate.semaphore:
	}
	return func() {
		gate.semaphore <- struct{}{}
		s.releasePromotionGateUser(candidateID, gate)
	}, nil
}

func (s *DiscoveryService) releasePromotionGateUser(candidateID domain.DiscoveryCandidateID, gate *discoveryPromotionGate) {
	s.promotionGatesMu.Lock()
	gate.users--
	if gate.users == 0 {
		delete(s.promotionGates, candidateID)
	}
	s.promotionGatesMu.Unlock()
}

func discoveryRepositories(repositories Repositories) port.DiscoveryRepositories {
	return port.DiscoveryRepositories{
		Discovery: repositories.Discovery, Locations: repositories.Locations,
		Monitors: repositories.Monitors, Health: repositories.Health,
	}
}

func (s *DiscoveryService) promoteWithRepositories(
	ctx context.Context,
	repositories port.DiscoveryRepositories,
	candidateID domain.DiscoveryCandidateID,
	command DiscoveryPromotionCommand,
) (DiscoveryPromotion, error) {
	if repositories.Discovery == nil || repositories.Locations == nil || repositories.Monitors == nil || repositories.Health == nil {
		return DiscoveryPromotion{}, errors.New("discovery promotion repositories are not configured")
	}
	candidate, err := repositories.Discovery.GetForUpdate(ctx, candidateID)
	if err != nil {
		return DiscoveryPromotion{}, err
	}
	if candidate.PromotedMonitorID != nil {
		return loadDiscoveryPromotionForCandidate(ctx, repositories, candidate)
	}
	if !candidate.Present {
		return DiscoveryPromotion{}, &ValidationError{Fields: map[string]string{"candidate": "stale candidate cannot be promoted"}}
	}
	if command.LocationID != candidate.LocationID {
		return DiscoveryPromotion{}, &ValidationError{Fields: map[string]string{"locationId": "must match candidate location"}}
	}
	if _, err := repositories.Locations.Get(ctx, command.LocationID); err != nil {
		return DiscoveryPromotion{}, err
	}
	probe, err := discoveryProbe(candidate)
	if err != nil {
		return DiscoveryPromotion{}, &ValidationError{Fields: map[string]string{"candidate": "target cannot be promoted"}}
	}
	now := s.now().UTC()
	monitorID := domain.MonitorID(s.newMonitorID())
	monitor, err := newConfiguredMonitor(monitorID, CreateMonitorCommand{
		Name: command.Name, Description: command.Description, Labels: command.Labels, DisplayOrder: command.DisplayOrder,
		Public: command.Public, LocationID: command.LocationID, RequiredLocation: command.RequiredLocation,
		Interval: command.Interval, Timeout: command.Timeout, FailureThreshold: command.FailureThreshold,
		RecoveryThreshold: command.RecoveryThreshold, Probe: probe,
	}, now)
	if err != nil {
		return DiscoveryPromotion{}, &ValidationError{Fields: map[string]string{"monitor": "contains invalid configuration"}}
	}
	if err := repositories.Monitors.Create(ctx, monitor); err != nil {
		return DiscoveryPromotion{}, err
	}
	assignment := port.MonitorLocation{MonitorID: monitor.ID, LocationID: command.LocationID, Required: command.RequiredLocation}
	if err := repositories.Monitors.AssignLocation(ctx, assignment); err != nil {
		return DiscoveryPromotion{}, err
	}
	if err := repositories.Health.UpsertLocation(ctx, domain.LocationHealth{MonitorID: monitor.ID, LocationID: command.LocationID, State: domain.HealthPending, LastTransitionAt: now}); err != nil {
		return DiscoveryPromotion{}, err
	}
	if err := repositories.Health.UpsertMonitor(ctx, domain.MonitorHealth{MonitorID: monitor.ID, State: domain.HealthPending, LastTransitionAt: now}); err != nil {
		return DiscoveryPromotion{}, err
	}
	linked, err := repositories.Discovery.LinkPromotion(ctx, candidate.ID, monitor.ID, now)
	if err != nil {
		return DiscoveryPromotion{}, err
	}
	if !linked {
		return DiscoveryPromotion{}, port.ErrConflict
	}
	candidate.PromotedMonitorID = &monitor.ID
	candidate.UpdatedAt = now
	return DiscoveryPromotion{Candidate: candidate.Clone(), Monitor: ConfiguredMonitor{
		Monitor: monitor, LocationID: command.LocationID, RequiredLocation: command.RequiredLocation,
	}}, nil
}

func loadDiscoveryPromotion(
	ctx context.Context,
	repositories port.DiscoveryRepositories,
	candidateID domain.DiscoveryCandidateID,
) (DiscoveryPromotion, error) {
	if repositories.Discovery == nil {
		return DiscoveryPromotion{}, errors.New("discovery repository is not configured")
	}
	candidate, err := repositories.Discovery.Get(ctx, candidateID)
	if err != nil {
		return DiscoveryPromotion{}, err
	}
	return loadDiscoveryPromotionForCandidate(ctx, repositories, candidate)
}

func loadDiscoveryPromotionForCandidate(
	ctx context.Context,
	repositories port.DiscoveryRepositories,
	candidate domain.DiscoveryCandidate,
) (DiscoveryPromotion, error) {
	if candidate.PromotedMonitorID == nil {
		return DiscoveryPromotion{}, port.ErrNotFound
	}
	monitor, err := repositories.Monitors.Get(ctx, *candidate.PromotedMonitorID)
	if err != nil {
		return DiscoveryPromotion{}, err
	}
	assignment, err := repositories.Monitors.GetAssignment(ctx, monitor.ID)
	if err != nil {
		return DiscoveryPromotion{}, err
	}
	return DiscoveryPromotion{Candidate: candidate.Clone(), Monitor: ConfiguredMonitor{
		Monitor: monitor, LocationID: assignment.LocationID, RequiredLocation: assignment.Required,
	}}, nil
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

func (s *DiscoveryService) List(
	ctx context.Context,
	filter port.DiscoveryFilter,
	page PageRequest,
) (Page[domain.DiscoveryCandidate], error) {
	if s == nil || s.store == nil || s.cursors == nil {
		return Page[domain.DiscoveryCandidate]{}, errors.New("discovery list service is not configured")
	}
	if filter.State != port.DiscoveryStateAll && filter.State != port.DiscoveryStatePending &&
		filter.State != port.DiscoveryStatePromoted {
		return Page[domain.DiscoveryCandidate]{}, &ValidationError{Fields: map[string]string{"state": "must be pending or promoted"}}
	}
	audience := CursorAudience{Endpoint: discoveryCursorEndpoint, Filter: map[string][]string{}}
	if filter.State != port.DiscoveryStateAll {
		audience.Filter["state"] = []string{string(filter.State)}
	}
	if filter.Present != nil {
		audience.Filter["present"] = []string{strconv.FormatBool(*filter.Present)}
	}
	limit := NormalizePageLimit(page.Limit)
	request := port.DiscoveryListRequest{Filter: filter, Limit: limit + 1}
	if page.Cursor != "" {
		key, err := s.cursors.DecodeFor(page.Cursor, audience, CursorShapeString)
		if err != nil {
			return Page[domain.DiscoveryCandidate]{}, err
		}
		request.After = domain.DiscoveryCandidateID(key.ID)
	}
	var rows []domain.DiscoveryCandidate
	err := s.store.DiscoveryView(ctx, func(ctx context.Context, repositories port.DiscoveryRepositories) error {
		var err error
		rows, err = repositories.Discovery.List(ctx, request)
		return err
	})
	if err != nil {
		return Page[domain.DiscoveryCandidate]{}, fmt.Errorf("list discovery candidates: %w", err)
	}
	rows, hasMore := trimPage(rows, limit)
	result := Page[domain.DiscoveryCandidate]{Items: make([]domain.DiscoveryCandidate, len(rows))}
	for index, candidate := range rows {
		result.Items[index] = candidate.Clone()
	}
	if !hasMore {
		return result, nil
	}
	last := rows[len(rows)-1]
	cursor, err := s.cursors.EncodeFor(audience, CursorKey{Sort: string(last.ID), ID: string(last.ID)}, CursorShapeString)
	if err != nil {
		return Page[domain.DiscoveryCandidate]{}, err
	}
	result.NextCursor = cursor
	return result, nil
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
