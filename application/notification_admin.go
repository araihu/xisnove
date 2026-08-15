package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

const maxAdminPageSize = port.MaxPageLimit

// NotificationChannelConfig is the plaintext form accepted at the use-case
// boundary. It exists only long enough to validate, encode, and seal it.
type NotificationChannelConfig struct {
	Kind               domain.NotificationChannelKind
	ShoutrrrServiceURL string
	AlertmanagerURL    string
	BearerToken        string
}

type PutNotificationChannelCommand struct {
	Name    string
	Enabled bool
	Config  NotificationChannelConfig
}

type PutNotificationRouteCommand struct {
	ID            domain.NotificationRouteID
	Name          string
	ChannelID     domain.NotificationChannelID
	MonitorID     *domain.MonitorID
	LabelMatchers map[string]string
	Actions       []domain.NotificationAction
	Severities    []domain.IncidentSeverity
	Template      string
	Enabled       bool
	Precedence    int32
}

type CreateMaintenanceCommand struct {
	MonitorID domain.MonitorID
	StartsAt  time.Time
	EndsAt    *time.Time
	Reason    string
	Principal Principal
}

type NotificationDeliveryDetail struct {
	Delivery port.NotificationOutboxRecord
	Attempts []port.NotificationDeliveryAttemptRecord
}

type NotificationAdminServiceConfig struct {
	Store  port.UnitOfWork
	Sealer port.ConfigSealer
	Now    func() time.Time
	NewID  func() string
}

// NotificationAdminService owns the administrative notification and
// maintenance use cases. HTTP and other driving adapters never receive direct
// repository access.
type NotificationAdminService struct {
	store  port.UnitOfWork
	sealer port.ConfigSealer
	now    func() time.Time
	newID  func() string
}

func NewNotificationAdminService(config NotificationAdminServiceConfig) *NotificationAdminService {
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &NotificationAdminService{
		store: config.Store, sealer: config.Sealer, now: now, newID: config.NewID,
	}
}

func (s *NotificationAdminService) CreateChannel(ctx context.Context, command PutNotificationChannelCommand) (domain.NotificationChannel, error) {
	if s.newID == nil {
		return domain.NotificationChannel{}, errors.New("create notification channel: identifier generator is required")
	}
	now := s.now().UTC()
	channel, err := domain.NewNotificationChannel(domain.NotificationChannelID(s.newID()), command.Name, command.Config.Kind, command.Enabled, now)
	if err != nil {
		return domain.NotificationChannel{}, notificationValidation("channel", "contains invalid configuration")
	}
	record, err := s.sealChannel(ctx, channel, command.Config)
	if err != nil {
		return domain.NotificationChannel{}, err
	}
	err = s.store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		return repositories.NotificationChannels.Create(ctx, record)
	})
	if err != nil {
		return domain.NotificationChannel{}, fmt.Errorf("create notification channel: %w", err)
	}
	return channel, nil
}

func (s *NotificationAdminService) GetChannel(ctx context.Context, id domain.NotificationChannelID) (domain.NotificationChannel, error) {
	var channel domain.NotificationChannel
	err := s.store.View(ctx, func(ctx context.Context, repositories port.Repositories) error {
		record, err := repositories.NotificationChannels.Get(ctx, id)
		channel = record.Channel
		return err
	})
	return channel, err
}

func (s *NotificationAdminService) ListChannels(ctx context.Context, limit, offset int) ([]domain.NotificationChannel, error) {
	limit, offset = boundedPage(limit, offset)
	var channels []domain.NotificationChannel
	err := s.store.View(ctx, func(ctx context.Context, repositories port.Repositories) error {
		records, err := repositories.NotificationChannels.List(ctx, limit, offset)
		if err != nil {
			return err
		}
		channels = make([]domain.NotificationChannel, len(records))
		for index := range records {
			channels[index] = records[index].Channel
		}
		return nil
	})
	return channels, err
}

func (s *NotificationAdminService) UpdateChannel(ctx context.Context, id domain.NotificationChannelID, command PutNotificationChannelCommand) (domain.NotificationChannel, error) {
	var updated domain.NotificationChannel
	err := s.store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		existing, err := repositories.NotificationChannels.Get(ctx, id)
		if err != nil {
			return err
		}
		updated, err = domain.NewNotificationChannel(id, command.Name, command.Config.Kind, command.Enabled, existing.Channel.CreatedAt)
		if err != nil {
			return notificationValidation("channel", "contains invalid configuration")
		}
		updated.UpdatedAt = s.now().UTC()
		record, err := s.sealChannel(ctx, updated, command.Config)
		if err != nil {
			return err
		}
		changed, err := repositories.NotificationChannels.Update(ctx, record)
		if err != nil {
			return err
		}
		if !changed {
			return port.ErrConflict
		}
		return nil
	})
	return updated, err
}

func (s *NotificationAdminService) DisableChannel(ctx context.Context, id domain.NotificationChannelID) error {
	return s.store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		changed, err := repositories.NotificationChannels.SetEnabled(ctx, id, false, s.now().UTC())
		if err != nil {
			return err
		}
		if !changed {
			return port.ErrNotFound
		}
		return nil
	})
}

func (s *NotificationAdminService) sealChannel(ctx context.Context, channel domain.NotificationChannel, config NotificationChannelConfig) (port.NotificationChannelRecord, error) {
	if s.sealer == nil {
		return port.NotificationChannelRecord{}, notificationValidation("configuration", "notification master key is not configured")
	}
	plaintext, err := encodeChannelConfig(config)
	if err != nil {
		return port.NotificationChannelRecord{}, err
	}
	defer clear(plaintext)
	sealed, err := s.sealer.Seal(ctx, port.ConfigIdentity{ChannelID: channel.ID, Kind: channel.Kind}, plaintext)
	if err != nil {
		return port.NotificationChannelRecord{}, fmt.Errorf("seal notification channel configuration: %w", err)
	}
	return port.NotificationChannelRecord{Channel: channel, EncryptedConfig: sealed.Ciphertext, KeyVersion: sealed.KeyVersion}, nil
}

func encodeChannelConfig(config NotificationChannelConfig) ([]byte, error) {
	var value any
	switch config.Kind {
	case domain.NotificationChannelShoutrrr:
		if strings.TrimSpace(config.ShoutrrrServiceURL) == "" {
			return nil, notificationValidation("configuration.serviceUrl", "must not be empty")
		}
		value = struct {
			ServiceURL string `json:"serviceUrl"`
		}{ServiceURL: config.ShoutrrrServiceURL}
	case domain.NotificationChannelAlertmanager:
		parsed, err := url.ParseRequestURI(config.AlertmanagerURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, notificationValidation("configuration.endpoint", "must be an absolute HTTP URL")
		}
		value = struct {
			Endpoint    string `json:"endpoint"`
			BearerToken string `json:"bearerToken,omitempty"`
		}{Endpoint: config.AlertmanagerURL, BearerToken: config.BearerToken}
	default:
		return nil, notificationValidation("configuration.kind", "is unsupported")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode notification channel configuration: %w", err)
	}
	return encoded, nil
}

func (s *NotificationAdminService) CreateRoute(ctx context.Context, command PutNotificationRouteCommand) (domain.NotificationRoute, error) {
	if s.newID == nil {
		return domain.NotificationRoute{}, errors.New("create notification route: identifier generator is required")
	}
	command.ID = domain.NotificationRouteID(s.newID())
	return s.putRoute(ctx, command, true)
}

func (s *NotificationAdminService) UpdateRoute(ctx context.Context, id domain.NotificationRouteID, command PutNotificationRouteCommand) (domain.NotificationRoute, error) {
	command.ID = id
	return s.putRoute(ctx, command, false)
}

func (s *NotificationAdminService) putRoute(ctx context.Context, command PutNotificationRouteCommand, create bool) (domain.NotificationRoute, error) {
	now := s.now().UTC()
	var route domain.NotificationRoute
	err := s.store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		createdAt := now
		if !create {
			existing, err := repositories.NotificationRoutes.Get(ctx, command.ID)
			if err != nil {
				return err
			}
			createdAt = existing.CreatedAt
		}
		if _, err := repositories.NotificationChannels.Get(ctx, command.ChannelID); err != nil {
			return err
		}
		if command.MonitorID != nil {
			if _, err := repositories.Monitors.Get(ctx, *command.MonitorID); err != nil {
				return err
			}
		}
		var err error
		route, err = domain.NewNotificationRoute(domain.NotificationRoute{
			ID: command.ID, Name: command.Name, ChannelID: command.ChannelID,
			MonitorID: command.MonitorID, LabelMatchers: command.LabelMatchers,
			Actions: command.Actions, Severities: command.Severities,
			Template: command.Template, Enabled: command.Enabled,
			Precedence: command.Precedence, CreatedAt: createdAt, UpdatedAt: now,
		})
		if err != nil {
			return notificationValidation("route", "contains invalid configuration")
		}
		if create {
			return repositories.NotificationRoutes.Create(ctx, route)
		}
		changed, err := repositories.NotificationRoutes.Update(ctx, route)
		if err != nil {
			return err
		}
		if !changed {
			return port.ErrConflict
		}
		return nil
	})
	return route, err
}

func (s *NotificationAdminService) GetRoute(ctx context.Context, id domain.NotificationRouteID) (domain.NotificationRoute, error) {
	var route domain.NotificationRoute
	err := s.store.View(ctx, func(ctx context.Context, repositories port.Repositories) error {
		var err error
		route, err = repositories.NotificationRoutes.Get(ctx, id)
		return err
	})
	return route, err
}

func (s *NotificationAdminService) ListRoutes(ctx context.Context, limit, offset int) ([]domain.NotificationRoute, error) {
	limit, offset = boundedPage(limit, offset)
	var routes []domain.NotificationRoute
	err := s.store.View(ctx, func(ctx context.Context, repositories port.Repositories) error {
		var err error
		routes, err = repositories.NotificationRoutes.List(ctx, limit, offset)
		return err
	})
	return routes, err
}

func (s *NotificationAdminService) DisableRoute(ctx context.Context, id domain.NotificationRouteID) error {
	return s.store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		changed, err := repositories.NotificationRoutes.SetEnabled(ctx, id, false, s.now().UTC())
		if err != nil {
			return err
		}
		if !changed {
			return port.ErrNotFound
		}
		return nil
	})
}

func (s *NotificationAdminService) ListDeliveries(ctx context.Context, limit, offset int) ([]port.NotificationOutboxRecord, error) {
	limit, offset = boundedPage(limit, offset)
	var deliveries []port.NotificationOutboxRecord
	err := s.store.View(ctx, func(ctx context.Context, repositories port.Repositories) error {
		var err error
		deliveries, err = repositories.NotificationOutbox.List(ctx, limit, offset)
		return err
	})
	return deliveries, err
}

func (s *NotificationAdminService) GetDelivery(ctx context.Context, id domain.NotificationDeliveryID) (NotificationDeliveryDetail, error) {
	var detail NotificationDeliveryDetail
	err := s.store.View(ctx, func(ctx context.Context, repositories port.Repositories) error {
		var err error
		detail.Delivery, err = repositories.NotificationOutbox.Get(ctx, id)
		if err != nil {
			return err
		}
		detail.Attempts, err = repositories.NotificationOutbox.ListAttempts(ctx, id)
		return err
	})
	return detail, err
}

func (s *NotificationAdminService) ReplayDelivery(ctx context.Context, id domain.NotificationDeliveryID) error {
	if s.newID == nil {
		return errors.New("replay notification delivery: identifier generator is required")
	}
	return s.store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		delivery, err := repositories.NotificationOutbox.Get(ctx, id)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		changed, err := repositories.NotificationOutbox.Replay(ctx, id, now)
		if err != nil {
			return err
		}
		if !changed {
			return port.ErrConflict
		}
		payload, err := json.Marshal(struct {
			PreviousState domain.DeliveryState `json:"previousState"`
			State         domain.DeliveryState `json:"state"`
		}{PreviousState: delivery.State, State: domain.DeliveryPending})
		if err != nil {
			return fmt.Errorf("encode notification replay audit: %w", err)
		}
		incidentID := delivery.RenderSnapshot.IncidentID
		return repositories.Audit.Append(ctx, port.AuditEventRecord{
			ID: s.newID(), Kind: "notification.delivery-replayed",
			SubjectKind: "notification-delivery", SubjectID: string(id),
			IncidentID: &incidentID, Payload: payload, CreatedAt: now,
		})
	})
}

func (s *NotificationAdminService) CreateMaintenance(ctx context.Context, command CreateMaintenanceCommand) (port.MaintenanceRecord, error) {
	if s.newID == nil {
		return port.MaintenanceRecord{}, errors.New("create maintenance: identifier generator is required")
	}
	interval, err := domain.NewMaintenanceInterval(domain.MaintenanceID(s.newID()), command.MonitorID, command.StartsAt, command.EndsAt, command.Reason)
	if err != nil {
		return port.MaintenanceRecord{}, notificationValidation("maintenance", "contains invalid configuration")
	}
	now := s.now().UTC()
	interval.CreatedAt = now
	record := port.MaintenanceRecord{Interval: interval, UpdatedAt: now}
	actor, userActionID := stateTickProvenance(command.Principal, s.newID)
	err = s.store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		monitor, err := repositories.Monitors.Get(ctx, command.MonitorID)
		if err != nil {
			return err
		}
		if err := repositories.Maintenance.Create(ctx, record); err != nil {
			return err
		}
		if err := appendMaintenanceAudit(ctx, repositories.Audit, s.newID(), "maintenance.created", record, now, actor, userActionID); err != nil {
			return err
		}
		if now.Before(interval.StartsAt) {
			return nil
		}
		_, err = appendMaintenanceActivationStateTick(
			ctx, repositories, monitor, interval.ID, maintenanceLifecycle(monitor, true),
			actor, userActionID, now,
		)
		return err
	})
	return record, err
}

func (s *NotificationAdminService) GetMaintenance(ctx context.Context, id domain.MaintenanceID) (port.MaintenanceRecord, error) {
	var record port.MaintenanceRecord
	err := s.store.View(ctx, func(ctx context.Context, repositories port.Repositories) error {
		var err error
		record, err = repositories.Maintenance.Get(ctx, id)
		return err
	})
	return record, err
}

func (s *NotificationAdminService) ListMaintenance(ctx context.Context, limit, offset int) ([]port.MaintenanceRecord, error) {
	limit, offset = boundedPage(limit, offset)
	var records []port.MaintenanceRecord
	err := s.store.View(ctx, func(ctx context.Context, repositories port.Repositories) error {
		var err error
		records, err = repositories.Maintenance.List(ctx, limit, offset)
		return err
	})
	return records, err
}

func (s *NotificationAdminService) EndMaintenance(ctx context.Context, id domain.MaintenanceID, principals ...Principal) (port.MaintenanceRecord, error) {
	if s.newID == nil {
		return port.MaintenanceRecord{}, errors.New("end maintenance: identifier generator is required")
	}
	now := s.now().UTC()
	var principal Principal
	if len(principals) > 0 {
		principal = principals[0]
	}
	var record port.MaintenanceRecord
	err := s.store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		existing, err := repositories.Maintenance.Get(ctx, id)
		if err != nil {
			return err
		}
		if now.Before(existing.Interval.StartsAt) {
			return notificationValidation("maintenance.startsAt", "cannot end maintenance before it starts")
		}
		if existing.Interval.EndsAt != nil && !existing.Interval.EndsAt.After(now) {
			record = existing
			return nil
		}
		monitor, err := repositories.Monitors.Get(ctx, existing.Interval.MonitorID)
		if err != nil {
			return err
		}
		activationActor, activationUserActionID, err := maintenanceActivationProvenance(ctx, repositories, existing.Interval.ID)
		if err != nil {
			return err
		}
		if _, err := appendMaintenanceActivationStateTick(
			ctx, repositories, monitor, existing.Interval.ID, maintenanceLifecycle(monitor, true),
			activationActor, activationUserActionID, now,
		); err != nil {
			return err
		}
		changed, err := repositories.Maintenance.End(ctx, id, now)
		if err != nil {
			return err
		}
		if !changed {
			return port.ErrConflict
		}
		record, err = repositories.Maintenance.Get(ctx, id)
		if err != nil {
			return err
		}
		actor, userActionID := stateTickProvenance(principal, s.newID)
		if err := appendMaintenanceAudit(ctx, repositories.Audit, s.newID(), "maintenance.ended", record, now, actor, userActionID); err != nil {
			return err
		}
		activeMaintenance, err := repositories.Maintenance.ListActive(ctx, monitor.ID, now)
		if err != nil {
			return err
		}
		lifecycle := maintenanceLifecycle(monitor, len(activeMaintenance) != 0)
		startTickID, _ := maintenanceStartStateTickIDs(record.Interval.ID)
		return appendMaintenanceStateTickCausal(
			ctx, repositories, monitor, lifecycle,
			actor, userActionID, &startTickID, now, s.newID,
		)
	})
	return record, err
}

func (s *NotificationAdminService) DeleteMaintenance(ctx context.Context, id domain.MaintenanceID) error {
	if s.newID == nil {
		return errors.New("delete maintenance: identifier generator is required")
	}
	now := s.now().UTC()
	return s.store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		record, err := repositories.Maintenance.Get(ctx, id)
		if err != nil {
			return err
		}
		if !now.Before(record.Interval.StartsAt) {
			return port.ErrConflict
		}
		changed, err := repositories.Maintenance.DeleteFuture(ctx, id, now)
		if err != nil {
			return err
		}
		if !changed {
			return port.ErrConflict
		}
		return appendMaintenanceAudit(ctx, repositories.Audit, s.newID(), "maintenance.deleted", record, now,
			domain.StateTickActor{Kind: domain.StateTickActorSystem}, nil)
	})
}

func appendMaintenanceAudit(
	ctx context.Context,
	repository port.AuditRepository,
	id string,
	kind string,
	record port.MaintenanceRecord,
	at time.Time,
	actor domain.StateTickActor,
	userActionID *string,
) error {
	payload, err := json.Marshal(maintenanceAuditPayload{
		MonitorID: record.Interval.MonitorID, StartsAt: record.Interval.StartsAt,
		EndsAt: record.Interval.EndsAt, Reason: record.Interval.Reason,
		ActorKind: actor.Kind, ActorID: actor.ID, UserActionID: cloneOptionalString(userActionID),
	})
	if err != nil {
		return fmt.Errorf("encode maintenance audit: %w", err)
	}
	return repository.Append(ctx, port.AuditEventRecord{
		ID: id, Kind: kind, SubjectKind: "maintenance",
		SubjectID: string(record.Interval.ID), Payload: payload, CreatedAt: at,
	})
}

func boundedPage(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 50
	}
	if limit > maxAdminPageSize {
		limit = maxAdminPageSize
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func notificationValidation(field, message string) error {
	return &ValidationError{Fields: map[string]string{field: message}}
}
