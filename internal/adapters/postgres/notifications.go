package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	application "github.com/araihu/xisnove/application/port"
	dbpostgres "github.com/araihu/xisnove/db/generated/postgres"
	"github.com/araihu/xisnove/domain"
)

type notificationChannelRepository struct{ queries *dbpostgres.Queries }

func (r *notificationChannelRepository) Create(ctx context.Context, record application.NotificationChannelRecord) error {
	err := r.queries.CreateNotificationChannel(ctx, dbpostgres.CreateNotificationChannelParams{
		ID: string(record.Channel.ID), Name: record.Channel.Name,
		Kind: string(record.Channel.Kind), EncryptedConfig: record.EncryptedConfig,
		KeyVersion: int32(record.KeyVersion), Enabled: record.Channel.Enabled,
		CreatedAt: record.Channel.CreatedAt.UTC(), UpdatedAt: record.Channel.UpdatedAt.UTC(),
	})
	return repositoryError("create notification channel", err)
}

func (r *notificationChannelRepository) Get(ctx context.Context, id domain.NotificationChannelID) (application.NotificationChannelRecord, error) {
	record, err := r.queries.GetNotificationChannel(ctx, string(id))
	if err != nil {
		return application.NotificationChannelRecord{}, repositoryError("get notification channel", err)
	}
	return mapNotificationChannel(record)
}

func (r *notificationChannelRepository) List(ctx context.Context, limit, offset int) ([]application.NotificationChannelRecord, error) {
	records, err := r.queries.ListNotificationChannels(ctx, dbpostgres.ListNotificationChannelsParams{RowOffset: int32(offset), RowLimit: int32(limit)})
	if err != nil {
		return nil, repositoryError("list notification channels", err)
	}
	result := make([]application.NotificationChannelRecord, 0, len(records))
	for _, record := range records {
		mapped, err := mapNotificationChannel(record)
		if err != nil {
			return nil, err
		}
		result = append(result, mapped)
	}
	return result, nil
}

func (r *notificationChannelRepository) Update(ctx context.Context, record application.NotificationChannelRecord) (bool, error) {
	affected, err := r.queries.UpdateNotificationChannel(ctx, dbpostgres.UpdateNotificationChannelParams{
		Name: record.Channel.Name, Kind: string(record.Channel.Kind),
		EncryptedConfig: record.EncryptedConfig, KeyVersion: int32(record.KeyVersion),
		Enabled: record.Channel.Enabled, UpdatedAt: record.Channel.UpdatedAt.UTC(),
		ID: string(record.Channel.ID),
	})
	return affected == 1, repositoryError("update notification channel", err)
}

func (r *notificationChannelRepository) SetEnabled(ctx context.Context, id domain.NotificationChannelID, enabled bool, at time.Time) (bool, error) {
	affected, err := r.queries.SetNotificationChannelEnabled(ctx, dbpostgres.SetNotificationChannelEnabledParams{
		Enabled: enabled, UpdatedAt: at.UTC(), ID: string(id),
	})
	return affected == 1, repositoryError("set notification channel enabled", err)
}

func (r *notificationChannelRepository) ListKeyVersions(ctx context.Context) ([]uint32, error) {
	versions, err := r.queries.ListNotificationChannelKeyVersions(ctx)
	if err != nil {
		return nil, repositoryError("list notification channel key versions", err)
	}
	result := make([]uint32, 0, len(versions))
	for _, version := range versions {
		if version <= 0 {
			return nil, errors.New("map notification channel key version: out of range")
		}
		result = append(result, uint32(version))
	}
	return result, nil
}

func (r *notificationChannelRepository) ListNeedingKeyVersion(
	ctx context.Context,
	active uint32,
	limit int,
) ([]application.NotificationChannelRecord, error) {
	if active > math.MaxInt32 {
		return nil, errors.New("list notification channels needing key rotation: active key version out of range")
	}
	records, err := r.queries.ListNotificationChannelsNeedingKeyVersion(
		ctx,
		dbpostgres.ListNotificationChannelsNeedingKeyVersionParams{
			ActiveKeyVersion: int32(active), RowLimit: int32(limit),
		},
	)
	if err != nil {
		return nil, repositoryError("list notification channels needing key rotation", err)
	}
	result := make([]application.NotificationChannelRecord, 0, len(records))
	for _, record := range records {
		mapped, err := mapNotificationChannel(record)
		if err != nil {
			return nil, err
		}
		result = append(result, mapped)
	}
	return result, nil
}

type notificationRouteRepository struct{ queries *dbpostgres.Queries }

func (r *notificationRouteRepository) Create(ctx context.Context, route domain.NotificationRoute) error {
	params, err := postgresRouteCreateParams(route)
	if err != nil {
		return err
	}
	return repositoryError("create notification route", r.queries.CreateNotificationRoute(ctx, params))
}

func (r *notificationRouteRepository) Get(ctx context.Context, id domain.NotificationRouteID) (domain.NotificationRoute, error) {
	record, err := r.queries.GetNotificationRoute(ctx, string(id))
	if err != nil {
		return domain.NotificationRoute{}, repositoryError("get notification route", err)
	}
	return mapNotificationRoute(record)
}

func (r *notificationRouteRepository) List(ctx context.Context, limit, offset int) ([]domain.NotificationRoute, error) {
	records, err := r.queries.ListNotificationRoutes(ctx, dbpostgres.ListNotificationRoutesParams{RowOffset: int32(offset), RowLimit: int32(limit)})
	if err != nil {
		return nil, repositoryError("list notification routes", err)
	}
	return mapNotificationRoutes(records)
}

func (r *notificationRouteRepository) ListEnabled(ctx context.Context) ([]domain.NotificationRoute, error) {
	records, err := r.queries.ListEnabledNotificationRoutes(ctx)
	if err != nil {
		return nil, repositoryError("list enabled notification routes", err)
	}
	return mapNotificationRoutes(records)
}

func (r *notificationRouteRepository) Update(ctx context.Context, route domain.NotificationRoute) (bool, error) {
	encoded, err := encodeRoute(route)
	if err != nil {
		return false, err
	}
	affected, err := r.queries.UpdateNotificationRoute(ctx, dbpostgres.UpdateNotificationRouteParams{
		RouteName: route.Name, ChannelID: string(route.ChannelID), MonitorID: nullableMonitorID(route.MonitorID),
		LabelMatchersJson: encoded.labels, ActionsJson: encoded.actions, SeveritiesJson: encoded.severities,
		Template: route.Template, Enabled: route.Enabled, Precedence: int32(route.Precedence),
		UpdatedAt: route.UpdatedAt.UTC(), ID: string(route.ID),
	})
	return affected == 1, repositoryError("update notification route", err)
}

func (r *notificationRouteRepository) SetEnabled(ctx context.Context, id domain.NotificationRouteID, enabled bool, at time.Time) (bool, error) {
	affected, err := r.queries.SetNotificationRouteEnabled(ctx, dbpostgres.SetNotificationRouteEnabledParams{
		Enabled: enabled, UpdatedAt: at.UTC(), ID: string(id),
	})
	return affected == 1, repositoryError("set notification route enabled", err)
}

type notificationOutboxRepository struct{ queries *dbpostgres.Queries }

func (r *notificationOutboxRepository) Insert(ctx context.Context, record application.NotificationOutboxRecord) (bool, error) {
	snapshot, err := json.Marshal(record.RenderSnapshot.Clone())
	if err != nil {
		return false, fmt.Errorf("encode notification snapshot: %w", err)
	}
	affected, err := r.queries.CreateNotificationOutbox(ctx, dbpostgres.CreateNotificationOutboxParams{
		ID: string(record.ID), IncidentEventID: record.IncidentEventID,
		RouteID: string(record.RouteID), ChannelID: string(record.ChannelID), DedupeKey: record.DedupeKey,
		RenderSnapshotJson: snapshot, State: string(record.State), AvailableAt: record.AvailableAt.UTC(),
		AttemptCount: int32(record.AttemptCount), SuppressedAt: nullablePGTimePtr(record.SuppressedAt),
		CreatedAt: record.CreatedAt.UTC(), UpdatedAt: record.UpdatedAt.UTC(),
	})
	return affected == 1, repositoryError("create notification outbox", err)
}

func (r *notificationOutboxRepository) Get(ctx context.Context, id domain.NotificationDeliveryID) (application.NotificationOutboxRecord, error) {
	record, err := r.queries.GetNotificationOutbox(ctx, string(id))
	if err != nil {
		return application.NotificationOutboxRecord{}, repositoryError("get notification outbox", err)
	}
	return mapNotificationOutbox(record)
}

func (r *notificationOutboxRepository) List(ctx context.Context, limit, offset int) ([]application.NotificationOutboxRecord, error) {
	records, err := r.queries.ListNotificationOutbox(ctx, dbpostgres.ListNotificationOutboxParams{RowOffset: int32(offset), RowLimit: int32(limit)})
	if err != nil {
		return nil, repositoryError("list notification outbox", err)
	}
	result := make([]application.NotificationOutboxRecord, 0, len(records))
	for _, record := range records {
		mapped, err := mapNotificationOutbox(record)
		if err != nil {
			return nil, err
		}
		result = append(result, mapped)
	}
	return result, nil
}

func (r *notificationOutboxRepository) ClaimDue(ctx context.Context, params application.ClaimNotificationParams) (application.NotificationOutboxRecord, error) {
	record, err := r.queries.ClaimDueNotificationOutbox(ctx, dbpostgres.ClaimDueNotificationOutboxParams{
		ClaimOwner: nullableString(params.Owner), ClaimTokenHash: params.ClaimTokenHash,
		ClaimExpiresAt: nullablePGTime(params.ClaimExpiresAt), Now: params.Now.UTC(),
	})
	if err != nil {
		return application.NotificationOutboxRecord{}, repositoryError("claim notification outbox", err)
	}
	return mapNotificationOutbox(record)
}

func (r *notificationOutboxRepository) AppendAttempt(ctx context.Context, attempt application.NotificationDeliveryAttemptRecord) error {
	err := r.queries.AppendNotificationDeliveryAttempt(ctx, dbpostgres.AppendNotificationDeliveryAttemptParams{
		ID: attempt.ID, OutboxID: string(attempt.OutboxID), Ordinal: int32(attempt.Ordinal),
		StartedAt: attempt.StartedAt.UTC(), FinishedAt: attempt.FinishedAt.UTC(),
		Outcome: string(attempt.Outcome), ErrorClass: nullableString(attempt.ErrorClass),
		Diagnostic: nullableString(attempt.Diagnostic), ProviderReceipt: nullableString(attempt.ProviderReceipt),
	})
	return repositoryError("append notification attempt", err)
}

func (r *notificationOutboxRepository) ListAttempts(ctx context.Context, id domain.NotificationDeliveryID) ([]application.NotificationDeliveryAttemptRecord, error) {
	records, err := r.queries.ListNotificationDeliveryAttempts(ctx, string(id))
	if err != nil {
		return nil, repositoryError("list notification attempts", err)
	}
	result := make([]application.NotificationDeliveryAttemptRecord, 0, len(records))
	for _, record := range records {
		mapped, err := mapNotificationAttempt(record)
		if err != nil {
			return nil, err
		}
		result = append(result, mapped)
	}
	return result, nil
}

func (r *notificationOutboxRepository) MarkDelivered(ctx context.Context, params application.FinalizeNotificationParams) (bool, error) {
	affected, err := r.queries.MarkNotificationDelivered(ctx, dbpostgres.MarkNotificationDeliveredParams{
		DeliveredAt: nullablePGTime(params.At), UpdatedAt: params.At.UTC(),
		ID: string(params.ID), ClaimTokenHash: params.ClaimTokenHash,
	})
	return affected == 1, repositoryError("mark notification delivered", err)
}

func (r *notificationOutboxRepository) MarkRetrying(ctx context.Context, params application.FinalizeNotificationParams) (bool, error) {
	affected, err := r.queries.MarkNotificationRetrying(ctx, dbpostgres.MarkNotificationRetryingParams{
		AvailableAt: params.AvailableAt.UTC(), ErrorClass: nullableString(params.ErrorClass),
		Diagnostic: nullableString(params.Diagnostic), UpdatedAt: params.At.UTC(),
		ID: string(params.ID), ClaimTokenHash: params.ClaimTokenHash,
	})
	return affected == 1, repositoryError("mark notification retrying", err)
}

func (r *notificationOutboxRepository) MarkPermanentFailure(ctx context.Context, params application.FinalizeNotificationParams) (bool, error) {
	affected, err := r.queries.MarkNotificationPermanentFailure(ctx, dbpostgres.MarkNotificationPermanentFailureParams{
		ErrorClass: nullableString(params.ErrorClass), Diagnostic: nullableString(params.Diagnostic),
		UpdatedAt: params.At.UTC(), ID: string(params.ID), ClaimTokenHash: params.ClaimTokenHash,
	})
	return affected == 1, repositoryError("mark notification permanent failure", err)
}

func (r *notificationOutboxRepository) MarkSuppressed(ctx context.Context, params application.FinalizeNotificationParams) (bool, error) {
	affected, err := r.queries.MarkNotificationSuppressed(ctx, dbpostgres.MarkNotificationSuppressedParams{
		SuppressedAt: nullablePGTime(params.At), UpdatedAt: params.At.UTC(),
		ID: string(params.ID), ClaimTokenHash: params.ClaimTokenHash,
	})
	return affected == 1, repositoryError("mark notification suppressed", err)
}

func (r *notificationOutboxRepository) ReleaseClaim(ctx context.Context, params application.FinalizeNotificationParams) (bool, error) {
	affected, err := r.queries.ReleaseNotificationClaim(ctx, dbpostgres.ReleaseNotificationClaimParams{
		AvailableAt: params.AvailableAt.UTC(), UpdatedAt: params.At.UTC(),
		ID: string(params.ID), ClaimTokenHash: params.ClaimTokenHash,
	})
	return affected == 1, repositoryError("release notification claim", err)
}

func (r *notificationOutboxRepository) Replay(ctx context.Context, id domain.NotificationDeliveryID, at time.Time) (bool, error) {
	affected, err := r.queries.ReplayNotificationOutbox(ctx, dbpostgres.ReplayNotificationOutboxParams{
		AvailableAt: at.UTC(), UpdatedAt: at.UTC(), ID: string(id),
	})
	return affected == 1, repositoryError("replay notification outbox", err)
}

type maintenanceRepository struct{ queries *dbpostgres.Queries }

func (r *maintenanceRepository) Create(ctx context.Context, record application.MaintenanceRecord) error {
	err := r.queries.CreateMaintenanceInterval(ctx, dbpostgres.CreateMaintenanceIntervalParams{
		ID: string(record.Interval.ID), MonitorID: string(record.Interval.MonitorID),
		StartsAt: record.Interval.StartsAt.UTC(), EndsAt: nullablePGTimePtr(record.Interval.EndsAt),
		Reason: record.Interval.Reason, CreatedAt: record.Interval.CreatedAt.UTC(),
		UpdatedAt: record.UpdatedAt.UTC(),
	})
	return repositoryError("create maintenance interval", err)
}

func (r *maintenanceRepository) Get(ctx context.Context, id domain.MaintenanceID) (application.MaintenanceRecord, error) {
	record, err := r.queries.GetMaintenanceInterval(ctx, string(id))
	if err != nil {
		return application.MaintenanceRecord{}, repositoryError("get maintenance interval", err)
	}
	return mapMaintenance(record)
}

func (r *maintenanceRepository) List(ctx context.Context, limit, offset int) ([]application.MaintenanceRecord, error) {
	records, err := r.queries.ListMaintenanceIntervals(ctx, dbpostgres.ListMaintenanceIntervalsParams{RowOffset: int32(offset), RowLimit: int32(limit)})
	if err != nil {
		return nil, repositoryError("list maintenance intervals", err)
	}
	return mapMaintenances(records)
}

func (r *maintenanceRepository) ListActive(ctx context.Context, monitorID domain.MonitorID, at time.Time) ([]application.MaintenanceRecord, error) {
	records, err := r.queries.ListActiveMaintenanceIntervals(ctx, dbpostgres.ListActiveMaintenanceIntervalsParams{
		MonitorID: string(monitorID), Now: at.UTC(),
	})
	if err != nil {
		return nil, repositoryError("list active maintenance intervals", err)
	}
	return mapMaintenances(records)
}

func (r *maintenanceRepository) End(ctx context.Context, id domain.MaintenanceID, at time.Time) (bool, error) {
	affected, err := r.queries.EndMaintenanceInterval(ctx, dbpostgres.EndMaintenanceIntervalParams{
		EndsAt: nullablePGTime(at), UpdatedAt: at.UTC(), ID: string(id),
	})
	return affected == 1, repositoryError("end maintenance interval", err)
}

func (r *maintenanceRepository) DeleteFuture(ctx context.Context, id domain.MaintenanceID, at time.Time) (bool, error) {
	affected, err := r.queries.DeleteFutureMaintenanceInterval(ctx, dbpostgres.DeleteFutureMaintenanceIntervalParams{ID: string(id), Now: at.UTC()})
	return affected == 1, repositoryError("delete future maintenance interval", err)
}

func (r *maintenanceRepository) ClaimEnded(ctx context.Context, params application.ClaimMaintenanceParams) (application.MaintenanceRecord, error) {
	record, err := r.queries.ClaimEndedMaintenanceInterval(ctx, dbpostgres.ClaimEndedMaintenanceIntervalParams{
		ClaimOwner: nullableString(params.Owner), ClaimTokenHash: params.ClaimTokenHash,
		ClaimExpiresAt: nullablePGTime(params.ClaimExpiresAt), Now: params.Now.UTC(),
	})
	if err != nil {
		return application.MaintenanceRecord{}, repositoryError("claim ended maintenance interval", err)
	}
	return mapMaintenance(record)
}

func (r *maintenanceRepository) MarkEndedProcessed(ctx context.Context, id domain.MaintenanceID, token []byte, at time.Time) (bool, error) {
	affected, err := r.queries.MarkEndedMaintenanceProcessed(ctx, dbpostgres.MarkEndedMaintenanceProcessedParams{
		ProcessedAt: nullablePGTime(at), ID: string(id), ClaimTokenHash: token,
	})
	return affected == 1, repositoryError("mark ended maintenance processed", err)
}

func (r *maintenanceRepository) ReleaseEndedClaim(ctx context.Context, id domain.MaintenanceID, token []byte, at time.Time) (bool, error) {
	affected, err := r.queries.ReleaseEndedMaintenanceClaim(ctx, dbpostgres.ReleaseEndedMaintenanceClaimParams{
		UpdatedAt: at.UTC(), ID: string(id), ClaimTokenHash: token,
	})
	return affected == 1, repositoryError("release ended maintenance claim", err)
}

type auditRepository struct{ queries *dbpostgres.Queries }

func (r *auditRepository) Append(ctx context.Context, event application.AuditEventRecord) error {
	err := r.queries.CreateAuditEvent(ctx, dbpostgres.CreateAuditEventParams{
		ID: event.ID, Kind: event.Kind, SubjectKind: event.SubjectKind, SubjectID: event.SubjectID,
		IncidentID: nullableIncidentID(event.IncidentID), PayloadJson: event.Payload,
		CreatedAt: event.CreatedAt.UTC(),
	})
	return repositoryError("append audit event", err)
}

func (r *auditRepository) ListByIncident(ctx context.Context, id domain.IncidentID) ([]application.AuditEventRecord, error) {
	records, err := r.queries.ListAuditEventsByIncident(ctx, nullableString(string(id)))
	if err != nil {
		return nil, repositoryError("list audit events", err)
	}
	result := make([]application.AuditEventRecord, 0, len(records))
	for _, record := range records {
		createdAt, err := postgresTime(record.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("map audit event: %w", err)
		}
		mapped := application.AuditEventRecord{
			ID: record.ID, Kind: record.Kind, SubjectKind: record.SubjectKind,
			SubjectID: record.SubjectID, Payload: append([]byte(nil), record.PayloadJson...), CreatedAt: createdAt,
		}
		if record.IncidentID.Valid {
			incidentID := domain.IncidentID(record.IncidentID.String)
			mapped.IncidentID = &incidentID
		}
		result = append(result, mapped)
	}
	return result, nil
}

type retentionRepository struct{ queries *dbpostgres.Queries }

func (r *retentionRepository) ClaimLease(ctx context.Context, lease application.OperationLeaseRecord, now time.Time) (application.OperationLeaseRecord, error) {
	record, err := r.queries.ClaimOperationLease(ctx, dbpostgres.ClaimOperationLeaseParams{
		LeaseKey: lease.Key, Owner: lease.Owner, TokenHash: lease.TokenHash,
		ExpiresAt: lease.ExpiresAt.UTC(), CursorJson: lease.Cursor, UpdatedAt: now.UTC(),
	})
	if err != nil {
		return application.OperationLeaseRecord{}, repositoryError("claim operation lease", err)
	}
	return mapOperationLease(record)
}

func (r *retentionRepository) UpdateLease(ctx context.Context, lease application.OperationLeaseRecord) (bool, error) {
	affected, err := r.queries.UpdateOperationLeaseCursor(ctx, dbpostgres.UpdateOperationLeaseCursorParams{
		CursorJson: lease.Cursor, ExpiresAt: lease.ExpiresAt.UTC(), UpdatedAt: lease.UpdatedAt.UTC(),
		LeaseKey: lease.Key, TokenHash: lease.TokenHash,
	})
	return affected == 1, repositoryError("update operation lease", err)
}

func (r *retentionRepository) ReleaseLease(ctx context.Context, key string, token []byte) (bool, error) {
	affected, err := r.queries.ReleaseOperationLease(ctx, dbpostgres.ReleaseOperationLeaseParams{LeaseKey: key, TokenHash: token})
	return affected == 1, repositoryError("release operation lease", err)
}

func (r *retentionRepository) ListAggregationResults(ctx context.Context, start, end, after time.Time, afterID string, limit int) ([]application.AggregationResultRecord, error) {
	// PostgreSQL compares the UUID cursor directly, so the empty first-page
	// sentinel used by the storage port must become the smallest valid UUID.
	// SQLite accepts the empty string and therefore does not need this mapping.
	if afterID == "" {
		afterID = "00000000-0000-0000-0000-000000000000"
	}
	records, err := r.queries.ListProbeResultsForDailyAggregation(ctx, dbpostgres.ListProbeResultsForDailyAggregationParams{
		StartsAt: start.UTC(), EndsAt: end.UTC(), AfterAt: after.UTC(),
		AfterID: afterID, RowLimit: int32(limit),
	})
	if err != nil {
		return nil, repositoryError("list aggregation results", err)
	}
	result := make([]application.AggregationResultRecord, 0, len(records))
	for _, record := range records {
		receivedAt, err := postgresTime(record.ReceivedAt)
		if err != nil {
			return nil, fmt.Errorf("map aggregation result: %w", err)
		}
		result = append(result, application.AggregationResultRecord{
			ID: record.ID, MonitorID: domain.MonitorID(record.MonitorID), ReceivedAt: receivedAt,
			Passed: record.Outcome == "passed", Latency: time.Duration(record.LatencyMs) * time.Millisecond,
		})
	}
	return result, nil
}

func (r *retentionRepository) UpsertDailyUptime(ctx context.Context, record application.DailyUptimeRecord) error {
	if record.Passing > math.MaxInt64 || record.Failing > math.MaxInt64 || record.Unknown > math.MaxInt64 || record.Observed.Milliseconds() < 0 {
		return errors.New("upsert daily uptime: value out of range")
	}
	err := r.queries.UpsertDailyUptime(ctx, dbpostgres.UpsertDailyUptimeParams{
		MonitorID: string(record.MonitorID), Day: record.Day.UTC(),
		PassingCount: int64(record.Passing), FailingCount: int64(record.Failing),
		UnknownCount: int64(record.Unknown), ObservedMs: record.Observed.Milliseconds(),
		UpdatedAt: record.UpdatedAt.UTC(),
	})
	return repositoryError("upsert daily uptime", err)
}

func (r *retentionRepository) ListDailyUptime(ctx context.Context, monitorID domain.MonitorID, start, end time.Time) ([]application.DailyUptimeRecord, error) {
	records, err := r.queries.ListDailyUptime(ctx, dbpostgres.ListDailyUptimeParams{
		MonitorID: string(monitorID), StartsOn: start.UTC(), EndsOn: end.UTC(),
	})
	if err != nil {
		return nil, repositoryError("list daily uptime", err)
	}
	result := make([]application.DailyUptimeRecord, 0, len(records))
	for _, record := range records {
		mapped, err := mapDailyUptime(record)
		if err != nil {
			return nil, err
		}
		result = append(result, mapped)
	}
	return result, nil
}

func (r *retentionRepository) DeleteExpiredResults(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	count, err := r.queries.DeleteExpiredProbeResults(ctx, dbpostgres.DeleteExpiredProbeResultsParams{Cutoff: cutoff.UTC(), RowLimit: int32(limit)})
	return count, repositoryError("delete expired probe results", err)
}

func (r *retentionRepository) DeleteExpiredDailyUptime(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	count, err := r.queries.DeleteExpiredDailyUptime(ctx, dbpostgres.DeleteExpiredDailyUptimeParams{CutoffDay: cutoff.UTC(), RowLimit: int32(limit)})
	return count, repositoryError("delete expired daily uptime", err)
}

func mapNotificationChannel(record dbpostgres.NotificationChannel) (application.NotificationChannelRecord, error) {
	if record.KeyVersion <= 0 {
		return application.NotificationChannelRecord{}, errors.New("map notification channel: key version out of range")
	}
	createdAt, err := postgresTime(record.CreatedAt)
	if err != nil {
		return application.NotificationChannelRecord{}, fmt.Errorf("map notification channel creation: %w", err)
	}
	channel, err := domain.NewNotificationChannel(domain.NotificationChannelID(record.ID), record.Name, domain.NotificationChannelKind(record.Kind), record.Enabled, createdAt)
	if err != nil {
		return application.NotificationChannelRecord{}, fmt.Errorf("map notification channel: %w", err)
	}
	channel.UpdatedAt, err = postgresTime(record.UpdatedAt)
	if err != nil {
		return application.NotificationChannelRecord{}, fmt.Errorf("map notification channel update: %w", err)
	}
	return application.NotificationChannelRecord{Channel: channel, EncryptedConfig: append([]byte(nil), record.EncryptedConfig...), KeyVersion: uint32(record.KeyVersion)}, nil
}

type encodedRoute struct{ labels, actions, severities []byte }

func encodeRoute(route domain.NotificationRoute) (encodedRoute, error) {
	labels, err := json.Marshal(route.LabelMatchers)
	if err != nil {
		return encodedRoute{}, fmt.Errorf("encode route labels: %w", err)
	}
	actions, err := json.Marshal(route.Actions)
	if err != nil {
		return encodedRoute{}, fmt.Errorf("encode route actions: %w", err)
	}
	severities, err := json.Marshal(route.Severities)
	if err != nil {
		return encodedRoute{}, fmt.Errorf("encode route severities: %w", err)
	}
	return encodedRoute{labels, actions, severities}, nil
}

func postgresRouteCreateParams(route domain.NotificationRoute) (dbpostgres.CreateNotificationRouteParams, error) {
	encoded, err := encodeRoute(route)
	if err != nil {
		return dbpostgres.CreateNotificationRouteParams{}, err
	}
	return dbpostgres.CreateNotificationRouteParams{
		ID: string(route.ID), RouteName: route.Name, ChannelID: string(route.ChannelID), MonitorID: nullableMonitorID(route.MonitorID),
		LabelMatchersJson: encoded.labels, ActionsJson: encoded.actions, SeveritiesJson: encoded.severities,
		Template: route.Template, Enabled: route.Enabled, Precedence: int32(route.Precedence),
		CreatedAt: route.CreatedAt.UTC(), UpdatedAt: route.UpdatedAt.UTC(),
	}, nil
}

func mapNotificationRoute(record dbpostgres.NotificationRoute) (domain.NotificationRoute, error) {
	var labels map[string]string
	var actions []domain.NotificationAction
	var severities []domain.IncidentSeverity
	if err := json.Unmarshal(record.LabelMatchersJson, &labels); err != nil {
		return domain.NotificationRoute{}, fmt.Errorf("map route labels: %w", err)
	}
	if err := json.Unmarshal(record.ActionsJson, &actions); err != nil {
		return domain.NotificationRoute{}, fmt.Errorf("map route actions: %w", err)
	}
	if err := json.Unmarshal(record.SeveritiesJson, &severities); err != nil {
		return domain.NotificationRoute{}, fmt.Errorf("map route severities: %w", err)
	}
	createdAt, err := postgresTime(record.CreatedAt)
	if err != nil {
		return domain.NotificationRoute{}, err
	}
	updatedAt, err := postgresTime(record.UpdatedAt)
	if err != nil {
		return domain.NotificationRoute{}, err
	}
	route := domain.NotificationRoute{
		ID: domain.NotificationRouteID(record.ID), Name: record.Name,
		ChannelID: domain.NotificationChannelID(record.ChannelID), LabelMatchers: labels,
		Actions: actions, Severities: severities, Template: record.Template,
		Enabled: record.Enabled, Precedence: int32(record.Precedence), CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	if record.MonitorID.Valid {
		monitorID := domain.MonitorID(record.MonitorID.String)
		route.MonitorID = &monitorID
	}
	return domain.NewNotificationRoute(route)
}

func mapNotificationRoutes(records []dbpostgres.NotificationRoute) ([]domain.NotificationRoute, error) {
	result := make([]domain.NotificationRoute, 0, len(records))
	for _, record := range records {
		mapped, err := mapNotificationRoute(record)
		if err != nil {
			return nil, err
		}
		result = append(result, mapped)
	}
	return result, nil
}

func mapNotificationOutbox(record dbpostgres.NotificationOutbox) (application.NotificationOutboxRecord, error) {
	if record.AttemptCount < 0 {
		return application.NotificationOutboxRecord{}, errors.New("map notification outbox: attempt count out of range")
	}
	var snapshot domain.RenderSnapshot
	if err := json.Unmarshal(record.RenderSnapshotJson, &snapshot); err != nil {
		return application.NotificationOutboxRecord{}, fmt.Errorf("map notification snapshot: %w", err)
	}
	availableAt, err := postgresTime(record.AvailableAt)
	if err != nil {
		return application.NotificationOutboxRecord{}, err
	}
	createdAt, err := postgresTime(record.CreatedAt)
	if err != nil {
		return application.NotificationOutboxRecord{}, err
	}
	updatedAt, err := postgresTime(record.UpdatedAt)
	if err != nil {
		return application.NotificationOutboxRecord{}, err
	}
	claimExpiresAt, err := postgresNullableTime(record.ClaimExpiresAt)
	if err != nil {
		return application.NotificationOutboxRecord{}, err
	}
	deliveredAt, err := postgresNullableTime(record.DeliveredAt)
	if err != nil {
		return application.NotificationOutboxRecord{}, err
	}
	suppressedAt, err := postgresNullableTime(record.SuppressedAt)
	if err != nil {
		return application.NotificationOutboxRecord{}, err
	}
	return application.NotificationOutboxRecord{
		ID: domain.NotificationDeliveryID(record.ID), IncidentEventID: record.IncidentEventID,
		RouteID: domain.NotificationRouteID(record.RouteID), ChannelID: domain.NotificationChannelID(record.ChannelID),
		DedupeKey: record.DedupeKey, RenderSnapshot: snapshot.Clone(), State: domain.DeliveryState(record.State),
		AvailableAt: availableAt, ClaimOwner: record.ClaimOwner.String,
		ClaimTokenHash: append([]byte(nil), record.ClaimTokenHash...), ClaimExpiresAt: claimExpiresAt,
		AttemptCount: uint32(record.AttemptCount), LastErrorClass: record.LastErrorClass.String,
		LastDiagnostic: record.LastDiagnostic.String, DeliveredAt: deliveredAt, SuppressedAt: suppressedAt,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}, nil
}

func mapNotificationAttempt(record dbpostgres.NotificationDeliveryAttempt) (application.NotificationDeliveryAttemptRecord, error) {
	if record.Ordinal <= 0 {
		return application.NotificationDeliveryAttemptRecord{}, errors.New("map notification attempt: ordinal out of range")
	}
	startedAt, err := postgresTime(record.StartedAt)
	if err != nil {
		return application.NotificationDeliveryAttemptRecord{}, err
	}
	finishedAt, err := postgresTime(record.FinishedAt)
	if err != nil {
		return application.NotificationDeliveryAttemptRecord{}, err
	}
	return application.NotificationDeliveryAttemptRecord{
		ID: record.ID, OutboxID: domain.NotificationDeliveryID(record.OutboxID), Ordinal: uint32(record.Ordinal),
		StartedAt: startedAt, FinishedAt: finishedAt, Outcome: application.NotificationAttemptOutcome(record.Outcome),
		ErrorClass: record.ErrorClass.String, Diagnostic: record.Diagnostic.String, ProviderReceipt: record.ProviderReceipt.String,
	}, nil
}

func mapMaintenance(record dbpostgres.MaintenanceInterval) (application.MaintenanceRecord, error) {
	startsAt, err := postgresTime(record.StartsAt)
	if err != nil {
		return application.MaintenanceRecord{}, err
	}
	endsAt, err := postgresNullableTime(record.EndsAt)
	if err != nil {
		return application.MaintenanceRecord{}, err
	}
	interval, err := domain.NewMaintenanceInterval(domain.MaintenanceID(record.ID), domain.MonitorID(record.MonitorID), startsAt, endsAt, record.Reason)
	if err != nil {
		return application.MaintenanceRecord{}, err
	}
	interval.CreatedAt, err = postgresTime(record.CreatedAt)
	if err != nil {
		return application.MaintenanceRecord{}, err
	}
	claimExpiresAt, err := postgresNullableTime(record.EndClaimExpiresAt)
	if err != nil {
		return application.MaintenanceRecord{}, err
	}
	processedAt, err := postgresNullableTime(record.EndedNotificationSentAt)
	if err != nil {
		return application.MaintenanceRecord{}, err
	}
	updatedAt, err := postgresTime(record.UpdatedAt)
	if err != nil {
		return application.MaintenanceRecord{}, err
	}
	interval.EndedNotificationSent = processedAt != nil
	return application.MaintenanceRecord{
		Interval: interval, EndClaimOwner: record.EndClaimOwner.String,
		EndClaimTokenHash: append([]byte(nil), record.EndClaimTokenHash...),
		EndClaimExpiresAt: claimExpiresAt, EndedNotificationSentAt: processedAt, UpdatedAt: updatedAt,
	}, nil
}

func mapMaintenances(records []dbpostgres.MaintenanceInterval) ([]application.MaintenanceRecord, error) {
	result := make([]application.MaintenanceRecord, 0, len(records))
	for _, record := range records {
		mapped, err := mapMaintenance(record)
		if err != nil {
			return nil, err
		}
		result = append(result, mapped)
	}
	return result, nil
}

func mapOperationLease(record dbpostgres.OperationLease) (application.OperationLeaseRecord, error) {
	expiresAt, err := postgresTime(record.ExpiresAt)
	if err != nil {
		return application.OperationLeaseRecord{}, err
	}
	updatedAt, err := postgresTime(record.UpdatedAt)
	if err != nil {
		return application.OperationLeaseRecord{}, err
	}
	return application.OperationLeaseRecord{
		Key: record.LeaseKey, Owner: record.Owner, TokenHash: append([]byte(nil), record.TokenHash...),
		ExpiresAt: expiresAt, Cursor: append([]byte(nil), record.CursorJson...), UpdatedAt: updatedAt,
	}, nil
}

func mapDailyUptime(record dbpostgres.DailyUptime) (application.DailyUptimeRecord, error) {
	if record.PassingCount < 0 || record.FailingCount < 0 || record.UnknownCount < 0 || record.ObservedMs < 0 {
		return application.DailyUptimeRecord{}, errors.New("map daily uptime: negative value")
	}
	day, err := postgresTime(record.Day)
	if err != nil {
		return application.DailyUptimeRecord{}, fmt.Errorf("map daily uptime day: %w", err)
	}
	updatedAt, err := postgresTime(record.UpdatedAt)
	if err != nil {
		return application.DailyUptimeRecord{}, err
	}
	return application.DailyUptimeRecord{
		MonitorID: domain.MonitorID(record.MonitorID), Day: day,
		Passing: uint64(record.PassingCount), Failing: uint64(record.FailingCount), Unknown: uint64(record.UnknownCount),
		Observed: time.Duration(record.ObservedMs) * time.Millisecond, UpdatedAt: updatedAt,
	}, nil
}

func nullableMonitorID(id *domain.MonitorID) sql.NullString {
	if id == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*id), Valid: true}
}

func nullableIncidentID(id *domain.IncidentID) sql.NullString {
	if id == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*id), Valid: true}
}

func nullablePGTime(value time.Time) sql.NullTime {
	return sql.NullTime{Time: value.UTC(), Valid: true}
}

func nullablePGTimePtr(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return nullablePGTime(*value)
}

func postgresTime(value time.Time) (time.Time, error) {
	return value.UTC(), nil
}

func postgresNullableTime(value sql.NullTime) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	result := value.Time.UTC()
	return &result, nil
}
