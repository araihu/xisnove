package application

import (
	"context"
	"fmt"
	"time"

	"github.com/araihu/xisnove/domain"
)

type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	return "validation failed"
}

type CreateLocationCommand struct {
	Name string
}

type CreateMonitorCommand struct {
	Name              string
	LocationID        domain.LocationID
	RequiredLocation  bool
	Interval          time.Duration
	Timeout           time.Duration
	FailureThreshold  uint16
	RecoveryThreshold uint16
	Probe             domain.ProbeDefinition
}

type ConfiguredMonitor struct {
	domain.Monitor
	LocationID       domain.LocationID
	RequiredLocation bool
}

type ConfigurationService struct {
	store UnitOfWork
	now   func() time.Time
	newID func() string
}

func NewConfigurationService(
	store UnitOfWork,
	now func() time.Time,
	newID func() string,
) *ConfigurationService {
	return &ConfigurationService{store: store, now: now, newID: newID}
}

func (s *ConfigurationService) CreateLocation(
	ctx context.Context,
	command CreateLocationCommand,
) (domain.Location, error) {
	location, err := domain.NewLocation(
		domain.LocationID(s.newID()),
		command.Name,
		s.now().UTC(),
	)
	if err != nil {
		return domain.Location{}, &ValidationError{
			Fields: map[string]string{"name": "must not be empty"},
		}
	}
	if err := s.store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		return repositories.Locations.Create(ctx, location)
	}); err != nil {
		return domain.Location{}, err
	}
	return location, nil
}

func (s *ConfigurationService) CreateMonitor(
	ctx context.Context,
	command CreateMonitorCommand,
) (ConfiguredMonitor, error) {
	now := s.now().UTC()
	monitor, err := newConfiguredMonitor(domain.MonitorID(s.newID()), command, now)
	if err != nil {
		return ConfiguredMonitor{}, &ValidationError{
			Fields: map[string]string{"monitor": "contains invalid configuration"},
		}
	}

	assignment := MonitorLocation{
		MonitorID:  monitor.ID,
		LocationID: command.LocationID,
		Required:   command.RequiredLocation,
	}
	err = s.store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
		if _, err := repositories.Locations.Get(ctx, command.LocationID); err != nil {
			return err
		}
		if err := repositories.Monitors.Create(ctx, monitor); err != nil {
			return err
		}
		if err := repositories.Monitors.AssignLocation(ctx, assignment); err != nil {
			return err
		}
		if err := repositories.Health.UpsertLocation(ctx, domain.LocationHealth{
			MonitorID:        monitor.ID,
			LocationID:       command.LocationID,
			State:            domain.HealthPending,
			LastTransitionAt: now,
		}); err != nil {
			return err
		}
		if err := repositories.Health.UpsertMonitor(ctx, domain.MonitorHealth{
			MonitorID:        monitor.ID,
			State:            domain.HealthPending,
			LastTransitionAt: now,
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return ConfiguredMonitor{}, fmt.Errorf("create monitor: %w", err)
	}

	return ConfiguredMonitor{
		Monitor:          monitor,
		LocationID:       command.LocationID,
		RequiredLocation: command.RequiredLocation,
	}, nil
}

func newConfiguredMonitor(
	id domain.MonitorID,
	command CreateMonitorCommand,
	now time.Time,
) (domain.Monitor, error) {
	if !probeVariantMatches(command.Probe) {
		return domain.Monitor{}, domain.ErrInvalidMonitor
	}
	switch command.Probe.Kind {
	case domain.MonitorKindHTTP:
		return domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
			ID: id, Name: command.Name, Interval: command.Interval, Timeout: command.Timeout,
			FailureThreshold:  command.FailureThreshold,
			RecoveryThreshold: command.RecoveryThreshold,
			HTTP:              command.Probe.HTTP, CreatedAt: now,
		})
	case domain.MonitorKindTCP:
		return domain.NewTCPMonitor(domain.NewTCPMonitorParams{
			ID: id, Name: command.Name, Interval: command.Interval, Timeout: command.Timeout,
			FailureThreshold:  command.FailureThreshold,
			RecoveryThreshold: command.RecoveryThreshold,
			TCP:               command.Probe.TCP, CreatedAt: now,
		})
	case domain.MonitorKindDNS:
		return domain.NewDNSMonitor(domain.NewDNSMonitorParams{
			ID: id, Name: command.Name, Interval: command.Interval, Timeout: command.Timeout,
			FailureThreshold:  command.FailureThreshold,
			RecoveryThreshold: command.RecoveryThreshold,
			DNS:               command.Probe.DNS, CreatedAt: now,
		})
	default:
		return domain.Monitor{}, domain.ErrInvalidMonitor
	}
}

func probeVariantMatches(probe domain.ProbeDefinition) bool {
	httpSet := probe.HTTP.Method != "" || probe.HTTP.URL != "" ||
		len(probe.HTTP.Headers) != 0 || len(probe.HTTP.Body) != 0 ||
		len(probe.HTTP.ExpectedStatus) != 0 || len(probe.HTTP.BodyContains) != 0 ||
		len(probe.HTTP.BodyDoesNotContain) != 0 || probe.HTTP.FollowRedirects ||
		probe.HTTP.TLS != nil
	tcpSet := probe.TCP.Host != "" || probe.TCP.Port != 0 ||
		len(probe.TCP.Send) != 0 || len(probe.TCP.Expect) != 0 ||
		probe.TCP.TLS != nil
	dnsSet := probe.DNS.Resolver != "" || probe.DNS.Name != "" ||
		probe.DNS.RecordType != "" || len(probe.DNS.ExpectedValues) != 0

	switch probe.Kind {
	case domain.MonitorKindHTTP:
		return httpSet && !tcpSet && !dnsSet
	case domain.MonitorKindTCP:
		return tcpSet && !httpSet && !dnsSet
	case domain.MonitorKindDNS:
		return dnsSet && !httpSet && !tcpSet
	default:
		return false
	}
}

func (s *ConfigurationService) GetMonitor(
	ctx context.Context,
	monitorID domain.MonitorID,
) (ConfiguredMonitor, error) {
	var configured ConfiguredMonitor
	err := s.store.View(ctx, func(ctx context.Context, repositories Repositories) error {
		monitor, err := repositories.Monitors.Get(ctx, monitorID)
		if err != nil {
			return err
		}
		assignment, err := repositories.Monitors.GetAssignment(ctx, monitorID)
		if err != nil {
			return err
		}
		configured = ConfiguredMonitor{
			Monitor:          monitor,
			LocationID:       assignment.LocationID,
			RequiredLocation: assignment.Required,
		}
		return nil
	})
	if err != nil {
		return ConfiguredMonitor{}, err
	}
	return configured, nil
}
