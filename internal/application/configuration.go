package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/araihu/xisnove/internal/domain"
)

var ErrConflict = errors.New("conflict")

type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	return "validation failed"
}

type CreateLocationCommand struct {
	Name string
}

type CreateHTTPMonitorCommand struct {
	Name              string
	LocationID        domain.LocationID
	RequiredLocation  bool
	Interval          time.Duration
	Timeout           time.Duration
	FailureThreshold  uint16
	RecoveryThreshold uint16
	HTTP              domain.HTTPProbe
}

type ConfiguredMonitor struct {
	domain.Monitor
	LocationID       domain.LocationID
	RequiredLocation bool
}

type ConfigurationService struct {
	store Store
	now   func() time.Time
	newID func() string
}

func NewConfigurationService(
	store Store,
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
	if err := s.store.Repositories().Locations.Create(ctx, location); err != nil {
		return domain.Location{}, err
	}
	return location, nil
}

func (s *ConfigurationService) CreateHTTPMonitor(
	ctx context.Context,
	command CreateHTTPMonitorCommand,
) (ConfiguredMonitor, error) {
	if _, err := s.store.Repositories().Locations.Get(ctx, command.LocationID); err != nil {
		return ConfiguredMonitor{}, err
	}

	now := s.now().UTC()
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID:                domain.MonitorID(s.newID()),
		Name:              command.Name,
		Interval:          command.Interval,
		Timeout:           command.Timeout,
		FailureThreshold:  command.FailureThreshold,
		RecoveryThreshold: command.RecoveryThreshold,
		HTTP:              command.HTTP,
		CreatedAt:         now,
	})
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
	err = s.store.WithinTx(ctx, func(repositories Repositories) error {
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
		return ConfiguredMonitor{}, fmt.Errorf("create HTTP monitor: %w", err)
	}

	return ConfiguredMonitor{
		Monitor:          monitor,
		LocationID:       command.LocationID,
		RequiredLocation: command.RequiredLocation,
	}, nil
}

func (s *ConfigurationService) GetMonitor(
	ctx context.Context,
	monitorID domain.MonitorID,
) (ConfiguredMonitor, error) {
	var configured ConfiguredMonitor
	err := s.store.WithinTx(ctx, func(repositories Repositories) error {
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
