package application

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/araihu/xisnove/domain"
)

type ProbeOutcome string

const (
	ProbePassed ProbeOutcome = "passed"
	ProbeFailed ProbeOutcome = "failed"
)

type ResultStatus string

const (
	ResultAccepted  ResultStatus = "accepted"
	ResultDuplicate ResultStatus = "duplicate"
)

type ProbeResultCommand struct {
	ID                  string
	RunID               domain.CheckRunID
	LeaseToken          string
	StartedAt           time.Time
	FinishedAt          time.Time
	Outcome             ProbeOutcome
	Latency             time.Duration
	ObservedStatus      *int
	BodyAssertionPassed *bool
	ErrorCode           string
	DiagnosticSample    string
	ObservedValues      []string
	TLSNotAfter         *time.Time
	ProtocolTimings     ProtocolTimings
}

type ResultAcknowledgement struct {
	ResultID string
	Status   ResultStatus
}

type ResultServiceConfig struct {
	Store         UnitOfWork
	Tokens        TokenIssuer
	Now           func() time.Time
	NewID         func() string
	LeaseDuration time.Duration
}

type ResultService struct {
	store         UnitOfWork
	tokens        TokenIssuer
	now           func() time.Time
	newID         func() string
	leaseDuration time.Duration
}

func NewResultService(config ResultServiceConfig) *ResultService {
	leaseDuration := config.LeaseDuration
	if leaseDuration <= 0 {
		leaseDuration = 45 * time.Second
	}
	return &ResultService{
		store: config.Store, tokens: config.Tokens, now: config.Now, newID: config.NewID,
		leaseDuration: leaseDuration,
	}
}

func (s *ResultService) UploadBatch(
	ctx context.Context,
	agentID domain.AgentID,
	commands []ProbeResultCommand,
) ([]ResultAcknowledgement, error) {
	if len(commands) == 0 || len(commands) > 100 {
		return nil, &ValidationError{
			Fields: map[string]string{"results": "must contain between 1 and 100 results"},
		}
	}

	acknowledgements := make([]ResultAcknowledgement, 0, len(commands))
	for _, command := range commands {
		status := ResultAccepted
		err := s.store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
			duplicate, err := resultExists(ctx, repositories.Results, command)
			if err != nil {
				return err
			}
			if duplicate {
				status = ResultDuplicate
				return nil
			}
			return s.ingest(ctx, repositories, agentID, command)
		})
		if err != nil {
			return nil, fmt.Errorf("ingest result %s: %w", command.ID, err)
		}
		acknowledgements = append(acknowledgements, ResultAcknowledgement{
			ResultID: command.ID, Status: status,
		})
	}
	return acknowledgements, nil
}

func resultExists(
	ctx context.Context,
	repository ResultRepository,
	command ProbeResultCommand,
) (bool, error) {
	if _, err := repository.GetByID(ctx, command.ID); err == nil {
		return true, nil
	} else if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	if _, err := repository.GetByRun(ctx, command.RunID); err == nil {
		return true, nil
	} else if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	return false, nil
}

func (s *ResultService) ingest(
	ctx context.Context,
	repositories Repositories,
	agentID domain.AgentID,
	command ProbeResultCommand,
) error {
	run, err := repositories.Runs.Get(ctx, command.RunID)
	if err != nil {
		return err
	}
	receivedAt := s.now().UTC()
	if err := validateResult(command, run, agentID, receivedAt); err != nil {
		return err
	}
	expectedHash := s.tokens.Hash(command.LeaseToken)
	if subtle.ConstantTimeCompare(expectedHash, run.LeaseTokenHash) != 1 {
		return &ValidationError{Fields: map[string]string{"leaseToken": "is invalid"}}
	}

	inserted, err := repositories.Results.Insert(ctx, ProbeResultRecord{
		ID:                  command.ID,
		RunID:               command.RunID,
		AgentID:             agentID,
		StartedAt:           command.StartedAt.UTC(),
		FinishedAt:          command.FinishedAt.UTC(),
		ReceivedAt:          receivedAt,
		Passed:              command.Outcome == ProbePassed,
		Latency:             command.Latency,
		ObservedStatus:      command.ObservedStatus,
		BodyAssertionPassed: command.BodyAssertionPassed,
		ErrorCode:           command.ErrorCode,
		DiagnosticSample:    command.DiagnosticSample,
		ObservedValues:      append([]string(nil), command.ObservedValues...),
		TLSNotAfter:         command.TLSNotAfter,
		ProtocolTimings:     command.ProtocolTimings,
	})
	if err != nil {
		return err
	}
	if !inserted {
		return ErrConflict
	}
	resolved, err := repositories.Runs.Resolve(
		ctx, run.ID, agentID, expectedHash, receivedAt,
	)
	if err != nil {
		return err
	}
	if !resolved {
		return &ValidationError{Fields: map[string]string{"runId": "lease is no longer active"}}
	}

	monitor, err := repositories.Monitors.Get(ctx, run.MonitorID)
	if err != nil {
		return err
	}
	locationHealth, err := repositories.Health.GetLocation(
		ctx, run.MonitorID, run.LocationID,
	)
	if err != nil {
		return err
	}
	locationHealth = domain.ApplyProbe(
		locationHealth,
		domain.ProbeObservation{
			Passed: command.Outcome == ProbePassed,
			At:     command.FinishedAt,
		},
		domain.Thresholds{
			Failures: monitor.FailureThreshold, Recoveries: monitor.RecoveryThreshold,
		},
	)
	locationHealth.StaleAt = command.FinishedAt.UTC().Add(
		2*monitor.Interval + monitor.Timeout + s.leaseDuration,
	)
	if err := repositories.Health.UpsertLocation(ctx, locationHealth); err != nil {
		return err
	}
	return projectAggregateAndIncident(
		ctx, repositories, run.MonitorID, command.FinishedAt, s.newID, false,
	)
}

func validateResult(
	command ProbeResultCommand,
	run RunRecord,
	agentID domain.AgentID,
	receivedAt time.Time,
) error {
	fields := make(map[string]string)
	if command.ID == "" {
		fields["resultId"] = "is required"
	}
	if run.Status != "leased" || run.LeaseAgentID != agentID ||
		run.LeaseExpiresAt == nil || run.LeaseExpiresAt.Add(30*time.Second).Before(receivedAt) {
		fields["runId"] = "lease is no longer active"
	}
	if command.StartedAt.Before(run.ScheduledFor.Add(-30*time.Second)) ||
		(run.LeaseExpiresAt != nil &&
			command.FinishedAt.After(run.LeaseExpiresAt.Add(30*time.Second))) ||
		command.FinishedAt.Before(command.StartedAt) {
		fields["timestamps"] = "are outside the accepted lease window"
	}
	if command.Latency < 0 {
		fields["latencyMillis"] = "must not be negative"
	}
	if len(command.DiagnosticSample) > 512 {
		fields["diagnosticSample"] = "must not exceed 512 bytes"
	}
	observedBytes := 0
	for _, value := range command.ObservedValues {
		observedBytes += len(value)
	}
	if len(command.ObservedValues) > 20 || observedBytes > 4<<10 {
		fields["observedValues"] = "must contain at most 20 values and 4096 bytes"
	}
	if command.ProtocolTimings.DNS < 0 ||
		command.ProtocolTimings.Connect < 0 ||
		command.ProtocolTimings.TLS < 0 ||
		command.ProtocolTimings.FirstByte < 0 {
		fields["protocolTimings"] = "must not be negative"
	}
	if command.Outcome != ProbePassed && command.Outcome != ProbeFailed {
		fields["outcome"] = "is unsupported"
	}
	if len(fields) != 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

type MonitorHealthView struct {
	Monitor   domain.MonitorHealth
	Locations []domain.LocationHealth
}

type HealthService struct {
	store UnitOfWork
}

func NewHealthService(store UnitOfWork) *HealthService {
	return &HealthService{store: store}
}

func (s *HealthService) GetMonitorHealth(
	ctx context.Context,
	monitorID domain.MonitorID,
) (MonitorHealthView, error) {
	var monitor domain.MonitorHealth
	var locations []domain.LocationHealth
	err := s.store.View(ctx, func(ctx context.Context, repositories Repositories) error {
		var err error
		monitor, err = repositories.Health.GetMonitor(ctx, monitorID)
		if err != nil {
			return err
		}
		locations, err = repositories.Health.ListRequiredLocations(ctx, monitorID)
		return err
	})
	if err != nil {
		return MonitorHealthView{}, err
	}
	return MonitorHealthView{Monitor: monitor, Locations: locations}, nil
}

func (s *HealthService) GetActiveIncident(
	ctx context.Context,
	monitorID domain.MonitorID,
) (*domain.Incident, error) {
	var incident *domain.Incident
	err := s.store.View(ctx, func(ctx context.Context, repositories Repositories) error {
		var err error
		incident, err = repositories.Incidents.GetActive(ctx, monitorID)
		return err
	})
	return incident, err
}
