package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

// ErrMaintenanceLeaseLost means another worker processed an ended interval.
var ErrMaintenanceLeaseLost = errors.New("maintenance end lease lost")

const (
	maintenanceActivationPageSize  = 1000
	maintenanceActivationScanLimit = 10000
)

// MaintenanceWorkerConfig defines bounded post-maintenance projection behavior.
type MaintenanceWorkerConfig struct {
	Store         port.UnitOfWork
	Tokens        TokenIssuer
	NewID         func() string
	Owner         string
	BatchSize     int
	LeaseDuration time.Duration
	PollInterval  time.Duration
	OnError       func(error)
}

// MaintenanceWorker emits one durable synthetic transition after maintenance.
type MaintenanceWorker struct{ config MaintenanceWorkerConfig }

// NewMaintenanceWorker validates configuration and applies safe defaults.
func NewMaintenanceWorker(config MaintenanceWorkerConfig) (*MaintenanceWorker, error) {
	if config.Store == nil || config.Tokens == nil || config.NewID == nil || strings.TrimSpace(config.Owner) == "" {
		return nil, errors.New("maintenance worker requires store, token issuer, identifier generator, and owner")
	}
	if config.BatchSize < 0 || config.LeaseDuration < 0 || config.PollInterval < 0 {
		return nil, errors.New("maintenance worker limits cannot be negative")
	}
	if config.BatchSize == 0 {
		config.BatchSize = 100
	}
	if config.LeaseDuration == 0 {
		config.LeaseDuration = 45 * time.Second
	}
	if config.PollInterval == 0 {
		config.PollInterval = time.Second
	}
	if config.BatchSize > 1000 {
		return nil, errors.New("maintenance worker batch size exceeds limit")
	}
	return &MaintenanceWorker{config: config}, nil
}

// Run polls until cancellation and reports recoverable cycle errors through OnError.
func (w *MaintenanceWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()
	for {
		if _, err := w.RunOnce(ctx); err != nil && ctx.Err() == nil && w.config.OnError != nil {
			w.config.OnError(err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunOnce activates due intervals and claims/processes at most BatchSize ended
// intervals. An ended interval discovered for the first time receives its
// deterministic start tick before the terminal tick in the same cycle.
func (w *MaintenanceWorker) RunOnce(ctx context.Context) (int, error) {
	processed, err := w.activateDue(ctx)
	if err != nil {
		return processed, err
	}
	for processed < w.config.BatchSize {
		record, tokenHash, err := w.claim(ctx)
		if errors.Is(err, port.ErrNotFound) {
			return processed, nil
		}
		if err != nil {
			return processed, err
		}
		if err := w.process(ctx, record, tokenHash); err != nil {
			w.release(ctx, record.Interval.ID, tokenHash)
			return processed, err
		}
		processed++
	}
	return processed, nil
}

// activateDue records scheduled maintenance starts at their effective
// transition. The deterministic tick identity makes this safe when multiple
// workers observe the same interval concurrently.
func (w *MaintenanceWorker) activateDue(ctx context.Context) (int, error) {
	activated := 0
	err := w.config.Store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		now, err := repositories.Runs.DatabaseNow(ctx)
		if err != nil {
			return err
		}
		activationCandidates := 0
		for offset := 0; offset < maintenanceActivationScanLimit && activationCandidates < w.config.BatchSize; {
			records, err := repositories.Maintenance.List(ctx, maintenanceActivationPageSize, offset)
			if err != nil {
				return err
			}
			for _, record := range records {
				startsDue := !record.Interval.StartsAt.After(now)
				endedUnprocessed := record.Interval.EndsAt != nil &&
					!record.Interval.EndsAt.After(now) && !record.Interval.EndedNotificationSent
				if !startsDue || (!record.Interval.ActiveAt(now) && !endedUnprocessed) {
					continue
				}
				activationCandidates++
				monitor, err := repositories.Monitors.Get(ctx, record.Interval.MonitorID)
				if err != nil {
					return err
				}
				actor, userActionID, err := maintenanceActivationProvenance(ctx, repositories, record.Interval.ID)
				if err != nil {
					return err
				}
				inserted, err := appendMaintenanceActivationStateTick(
					ctx, repositories, monitor, record.Interval.ID,
					maintenanceLifecycle(monitor, true),
					actor, userActionID,
					// The worker owns the effective transition, so delayed discovery
					// is recorded at the observation time rather than scheduled time.
					now,
				)
				if err != nil {
					return err
				}
				if inserted && !endedUnprocessed {
					activated++
				}
				if activationCandidates >= w.config.BatchSize {
					break
				}
			}
			if len(records) < maintenanceActivationPageSize {
				break
			}
			offset += len(records)
		}
		return nil
	})
	return activated, err
}

func (w *MaintenanceWorker) claim(ctx context.Context) (port.MaintenanceRecord, []byte, error) {
	token, err := w.config.Tokens.New()
	if err != nil {
		return port.MaintenanceRecord{}, nil, fmt.Errorf("create maintenance claim token: %w", err)
	}
	var record port.MaintenanceRecord
	err = w.config.Store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		now, err := repositories.Runs.DatabaseNow(ctx)
		if err != nil {
			return err
		}
		record, err = repositories.Maintenance.ClaimEnded(ctx, port.ClaimMaintenanceParams{
			Owner: w.config.Owner, ClaimTokenHash: token.Hash,
			ClaimExpiresAt: now.Add(w.config.LeaseDuration), Now: now,
		})
		return err
	})
	return record, append([]byte(nil), token.Hash...), err
}

func (w *MaintenanceWorker) process(ctx context.Context, record port.MaintenanceRecord, tokenHash []byte) error {
	return w.config.Store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		now, err := repositories.Runs.DatabaseNow(ctx)
		if err != nil {
			return err
		}
		health, err := repositories.Health.GetMonitor(ctx, record.Interval.MonitorID)
		if err != nil {
			return err
		}
		monitor, err := repositories.Monitors.Get(ctx, record.Interval.MonitorID)
		if err != nil {
			return err
		}
		if domain.ShouldNotifyAfterMaintenance(health.State, record.Interval.EndedNotificationSent) {
			incident, err := repositories.Incidents.GetActive(ctx, record.Interval.MonitorID)
			if err != nil {
				return err
			}
			if incident == nil {
				return errors.New("unhealthy monitor has no active incident after maintenance")
			}
			event := domain.IncidentEvent{
				ID: w.config.NewID(), IncidentID: incident.ID,
				Action:        domain.NotificationMaintenanceEnded,
				PreviousState: incident.State, State: incident.State,
				Severity: incident.Severity, CreatedAt: now,
			}
			if err := recordIncidentEvent(ctx, repositories, *incident, monitor, event, now, w.config.NewID); err != nil {
				return err
			}
		}
		activeMaintenance, err := repositories.Maintenance.ListActive(ctx, monitor.ID, now)
		if err != nil {
			return err
		}
		lifecycle := maintenanceLifecycle(monitor, len(activeMaintenance) != 0)
		if err := appendMaintenanceStateTick(
			ctx, repositories, monitor, lifecycle,
			domain.StateTickActor{Kind: domain.StateTickActorSystem}, nil, now, w.config.NewID,
		); err != nil {
			return err
		}
		changed, err := repositories.Maintenance.MarkEndedProcessed(ctx, record.Interval.ID, tokenHash, now)
		if err != nil {
			return err
		}
		if !changed {
			return ErrMaintenanceLeaseLost
		}
		payload, err := json.Marshal(struct {
			MonitorState domain.HealthState `json:"monitorState"`
			Notified     bool               `json:"notified"`
		}{MonitorState: health.State, Notified: domain.ShouldNotifyAfterMaintenance(health.State, record.Interval.EndedNotificationSent)})
		if err != nil {
			return err
		}
		return repositories.Audit.Append(ctx, port.AuditEventRecord{
			ID: w.config.NewID(), Kind: "maintenance.processed",
			SubjectKind: "maintenance", SubjectID: string(record.Interval.ID),
			Payload: payload, CreatedAt: now,
		})
	})
}

func (w *MaintenanceWorker) release(ctx context.Context, id domain.MaintenanceID, tokenHash []byte) {
	_ = w.config.Store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		now, err := repositories.Runs.DatabaseNow(ctx)
		if err != nil {
			return err
		}
		_, err = repositories.Maintenance.ReleaseEndedClaim(ctx, id, tokenHash, now)
		return err
	})
}
