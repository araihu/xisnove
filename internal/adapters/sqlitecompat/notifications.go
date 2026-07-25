package sqlitecompat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	application "github.com/araihu/xisnove/application/port"
	dbsqlite "github.com/araihu/xisnove/db/generated/sqlite"
	"github.com/araihu/xisnove/domain"
)

type notificationChannelRepository struct{ queries *dbsqlite.Queries }

func (r *notificationChannelRepository) Create(ctx context.Context, record application.NotificationChannelRecord) error {
	err := r.queries.CreateNotificationChannel(ctx, dbsqlite.CreateNotificationChannelParams{
		ID: string(record.Channel.ID), Name: record.Channel.Name,
		Kind: string(record.Channel.Kind), EncryptedConfig: record.EncryptedConfig,
		KeyVersion: int64(record.KeyVersion), Enabled: boolInt(record.Channel.Enabled),
		CreatedAt: formatTime(record.Channel.CreatedAt), UpdatedAt: formatTime(record.Channel.UpdatedAt),
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
	records, err := r.queries.ListNotificationChannels(ctx, dbsqlite.ListNotificationChannelsParams{Limit: int64(limit), Offset: int64(offset)})
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
	affected, err := r.queries.UpdateNotificationChannel(ctx, dbsqlite.UpdateNotificationChannelParams{
		Name: record.Channel.Name, Kind: string(record.Channel.Kind),
		EncryptedConfig: record.EncryptedConfig, KeyVersion: int64(record.KeyVersion),
		Enabled: boolInt(record.Channel.Enabled), UpdatedAt: formatTime(record.Channel.UpdatedAt),
		ID: string(record.Channel.ID),
	})
	return affected == 1, repositoryError("update notification channel", err)
}

func (r *notificationChannelRepository) SetEnabled(ctx context.Context, id domain.NotificationChannelID, enabled bool, at time.Time) (bool, error) {
	affected, err := r.queries.SetNotificationChannelEnabled(ctx, dbsqlite.SetNotificationChannelEnabledParams{
		Enabled: boolInt(enabled), UpdatedAt: formatTime(at), ID: string(id),
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
		if version <= 0 || version > math.MaxUint32 {
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
	records, err := r.queries.ListNotificationChannelsNeedingKeyVersion(
		ctx,
		dbsqlite.ListNotificationChannelsNeedingKeyVersionParams{
			ActiveKeyVersion: int64(active), RowLimit: int64(limit),
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

type notificationRouteRepository struct{ queries *dbsqlite.Queries }

func (r *notificationRouteRepository) Create(ctx context.Context, route domain.NotificationRoute) error {
	params, err := sqliteRouteCreateParams(route)
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
	records, err := r.queries.ListNotificationRoutes(ctx, dbsqlite.ListNotificationRoutesParams{Limit: int64(limit), Offset: int64(offset)})
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
	affected, err := r.queries.UpdateNotificationRoute(ctx, dbsqlite.UpdateNotificationRouteParams{
		Name: route.Name, ChannelID: string(route.ChannelID), MonitorID: nullableMonitorID(route.MonitorID),
		LabelMatchersJson: encoded.labels, ActionsJson: encoded.actions, SeveritiesJson: encoded.severities,
		Template: route.Template, Enabled: boolInt(route.Enabled), Precedence: int64(route.Precedence),
		UpdatedAt: formatTime(route.UpdatedAt), ID: string(route.ID),
	})
	return affected == 1, repositoryError("update notification route", err)
}

func (r *notificationRouteRepository) SetEnabled(ctx context.Context, id domain.NotificationRouteID, enabled bool, at time.Time) (bool, error) {
	affected, err := r.queries.SetNotificationRouteEnabled(ctx, dbsqlite.SetNotificationRouteEnabledParams{
		Enabled: boolInt(enabled), UpdatedAt: formatTime(at), ID: string(id),
	})
	return affected == 1, repositoryError("set notification route enabled", err)
}

type notificationOutboxRepository struct{ queries *dbsqlite.Queries }

func (r *notificationOutboxRepository) Insert(ctx context.Context, record application.NotificationOutboxRecord) (bool, error) {
	snapshot, err := json.Marshal(record.RenderSnapshot.Clone())
	if err != nil {
		return false, fmt.Errorf("encode notification snapshot: %w", err)
	}
	affected, err := r.queries.CreateNotificationOutbox(ctx, dbsqlite.CreateNotificationOutboxParams{
		ID: string(record.ID), IncidentEventID: record.IncidentEventID,
		RouteID: string(record.RouteID), ChannelID: string(record.ChannelID), DedupeKey: record.DedupeKey,
		RenderSnapshotJson: snapshot, State: string(record.State), AvailableAt: formatTime(record.AvailableAt),
		AttemptCount: int64(record.AttemptCount), SuppressedAt: nullableTime(record.SuppressedAt),
		CreatedAt: formatTime(record.CreatedAt), UpdatedAt: formatTime(record.UpdatedAt),
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
	records, err := r.queries.ListNotificationOutbox(ctx, dbsqlite.ListNotificationOutboxParams{Limit: int64(limit), Offset: int64(offset)})
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
	record, err := r.queries.ClaimDueNotificationOutbox(ctx, dbsqlite.ClaimDueNotificationOutboxParams{
		ClaimOwner: nullableString(params.Owner), ClaimTokenHash: params.ClaimTokenHash,
		ClaimExpiresAt: nullableTimeValue(params.ClaimExpiresAt), Now: formatTime(params.Now),
	})
	if err != nil {
		return application.NotificationOutboxRecord{}, repositoryError("claim notification outbox", err)
	}
	return mapNotificationOutbox(record)
}

func (r *notificationOutboxRepository) AppendAttempt(ctx context.Context, attempt application.NotificationDeliveryAttemptRecord) error {
	err := r.queries.AppendNotificationDeliveryAttempt(ctx, dbsqlite.AppendNotificationDeliveryAttemptParams{
		ID: attempt.ID, OutboxID: string(attempt.OutboxID), Ordinal: int64(attempt.Ordinal),
		StartedAt: formatTime(attempt.StartedAt), FinishedAt: formatTime(attempt.FinishedAt),
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
	affected, err := r.queries.MarkNotificationDelivered(ctx, dbsqlite.MarkNotificationDeliveredParams{
		DeliveredAt: nullableTimeValue(params.At), UpdatedAt: formatTime(params.At),
		ID: string(params.ID), ClaimTokenHash: params.ClaimTokenHash,
	})
	return affected == 1, repositoryError("mark notification delivered", err)
}

func (r *notificationOutboxRepository) MarkRetrying(ctx context.Context, params application.FinalizeNotificationParams) (bool, error) {
	affected, err := r.queries.MarkNotificationRetrying(ctx, dbsqlite.MarkNotificationRetryingParams{
		AvailableAt: formatTime(params.AvailableAt), ErrorClass: nullableString(params.ErrorClass),
		Diagnostic: nullableString(params.Diagnostic), UpdatedAt: formatTime(params.At),
		ID: string(params.ID), ClaimTokenHash: params.ClaimTokenHash,
	})
	return affected == 1, repositoryError("mark notification retrying", err)
}

func (r *notificationOutboxRepository) MarkPermanentFailure(ctx context.Context, params application.FinalizeNotificationParams) (bool, error) {
	affected, err := r.queries.MarkNotificationPermanentFailure(ctx, dbsqlite.MarkNotificationPermanentFailureParams{
		ErrorClass: nullableString(params.ErrorClass), Diagnostic: nullableString(params.Diagnostic),
		UpdatedAt: formatTime(params.At), ID: string(params.ID), ClaimTokenHash: params.ClaimTokenHash,
	})
	return affected == 1, repositoryError("mark notification permanent failure", err)
}

func (r *notificationOutboxRepository) MarkSuppressed(ctx context.Context, params application.FinalizeNotificationParams) (bool, error) {
	affected, err := r.queries.MarkNotificationSuppressed(ctx, dbsqlite.MarkNotificationSuppressedParams{
		SuppressedAt: nullableTimeValue(params.At), UpdatedAt: formatTime(params.At),
		ID: string(params.ID), ClaimTokenHash: params.ClaimTokenHash,
	})
	return affected == 1, repositoryError("mark notification suppressed", err)
}

func (r *notificationOutboxRepository) ReleaseClaim(ctx context.Context, params application.FinalizeNotificationParams) (bool, error) {
	affected, err := r.queries.ReleaseNotificationClaim(ctx, dbsqlite.ReleaseNotificationClaimParams{
		AvailableAt: formatTime(params.AvailableAt), UpdatedAt: formatTime(params.At),
		ID: string(params.ID), ClaimTokenHash: params.ClaimTokenHash,
	})
	return affected == 1, repositoryError("release notification claim", err)
}

func (r *notificationOutboxRepository) Replay(ctx context.Context, id domain.NotificationDeliveryID, at time.Time) (bool, error) {
	affected, err := r.queries.ReplayNotificationOutbox(ctx, dbsqlite.ReplayNotificationOutboxParams{
		AvailableAt: formatTime(at), UpdatedAt: formatTime(at), ID: string(id),
	})
	return affected == 1, repositoryError("replay notification outbox", err)
}

type maintenanceRepository struct{ queries *dbsqlite.Queries }

func (r *maintenanceRepository) Create(ctx context.Context, record application.MaintenanceRecord) error {
	err := r.queries.CreateMaintenanceInterval(ctx, dbsqlite.CreateMaintenanceIntervalParams{
		ID: string(record.Interval.ID), MonitorID: string(record.Interval.MonitorID),
		StartsAt: formatTime(record.Interval.StartsAt), EndsAt: nullableTime(record.Interval.EndsAt),
		Reason: record.Interval.Reason, CreatedAt: formatTime(record.Interval.CreatedAt),
		UpdatedAt: formatTime(record.UpdatedAt),
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
	records, err := r.queries.ListMaintenanceIntervals(ctx, dbsqlite.ListMaintenanceIntervalsParams{Limit: int64(limit), Offset: int64(offset)})
	if err != nil {
		return nil, repositoryError("list maintenance intervals", err)
	}
	return mapMaintenances(records)
}

func (r *maintenanceRepository) ListActive(ctx context.Context, monitorID domain.MonitorID, at time.Time) ([]application.MaintenanceRecord, error) {
	records, err := r.queries.ListActiveMaintenanceIntervals(ctx, dbsqlite.ListActiveMaintenanceIntervalsParams{
		MonitorID: string(monitorID), Now: formatTime(at),
	})
	if err != nil {
		return nil, repositoryError("list active maintenance intervals", err)
	}
	return mapMaintenances(records)
}

func (r *maintenanceRepository) End(ctx context.Context, id domain.MaintenanceID, at time.Time) (bool, error) {
	affected, err := r.queries.EndMaintenanceInterval(ctx, dbsqlite.EndMaintenanceIntervalParams{
		EndsAt: nullableTimeValue(at), UpdatedAt: formatTime(at), ID: string(id),
	})
	return affected == 1, repositoryError("end maintenance interval", err)
}

func (r *maintenanceRepository) DeleteFuture(ctx context.Context, id domain.MaintenanceID, at time.Time) (bool, error) {
	affected, err := r.queries.DeleteFutureMaintenanceInterval(ctx, dbsqlite.DeleteFutureMaintenanceIntervalParams{ID: string(id), Now: formatTime(at)})
	return affected == 1, repositoryError("delete future maintenance interval", err)
}

func (r *maintenanceRepository) ClaimEnded(ctx context.Context, params application.ClaimMaintenanceParams) (application.MaintenanceRecord, error) {
	record, err := r.queries.ClaimEndedMaintenanceInterval(ctx, dbsqlite.ClaimEndedMaintenanceIntervalParams{
		ClaimOwner: nullableString(params.Owner), ClaimTokenHash: params.ClaimTokenHash,
		ClaimExpiresAt: nullableTimeValue(params.ClaimExpiresAt), Now: formatTime(params.Now),
	})
	if err != nil {
		return application.MaintenanceRecord{}, repositoryError("claim ended maintenance interval", err)
	}
	return mapMaintenance(record)
}

func (r *maintenanceRepository) MarkEndedProcessed(ctx context.Context, id domain.MaintenanceID, token []byte, at time.Time) (bool, error) {
	affected, err := r.queries.MarkEndedMaintenanceProcessed(ctx, dbsqlite.MarkEndedMaintenanceProcessedParams{
		ProcessedAt: nullableTimeValue(at), ID: string(id), ClaimTokenHash: token,
	})
	return affected == 1, repositoryError("mark ended maintenance processed", err)
}

func (r *maintenanceRepository) ReleaseEndedClaim(ctx context.Context, id domain.MaintenanceID, token []byte, at time.Time) (bool, error) {
	affected, err := r.queries.ReleaseEndedMaintenanceClaim(ctx, dbsqlite.ReleaseEndedMaintenanceClaimParams{
		UpdatedAt: formatTime(at), ID: string(id), ClaimTokenHash: token,
	})
	return affected == 1, repositoryError("release ended maintenance claim", err)
}

type auditRepository struct{ queries *dbsqlite.Queries }

func (r *auditRepository) Append(ctx context.Context, event application.AuditEventRecord) error {
	err := r.queries.CreateAuditEvent(ctx, dbsqlite.CreateAuditEventParams{
		ID: event.ID, Kind: event.Kind, SubjectKind: event.SubjectKind, SubjectID: event.SubjectID,
		IncidentID: nullableIncidentID(event.IncidentID), PayloadJson: event.Payload,
		CreatedAt: formatTime(event.CreatedAt),
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
		createdAt, err := parseTime(record.CreatedAt)
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

type retentionRepository struct{ queries *dbsqlite.Queries }

func (r *retentionRepository) ClaimLease(ctx context.Context, lease application.OperationLeaseRecord, now time.Time) (application.OperationLeaseRecord, error) {
	record, err := r.queries.ClaimOperationLease(ctx, dbsqlite.ClaimOperationLeaseParams{
		LeaseKey: lease.Key, Owner: lease.Owner, TokenHash: lease.TokenHash,
		ExpiresAt: formatTime(lease.ExpiresAt), CursorJson: lease.Cursor, UpdatedAt: formatTime(now),
	})
	if err != nil {
		return application.OperationLeaseRecord{}, repositoryError("claim operation lease", err)
	}
	return mapOperationLease(record)
}

func (r *retentionRepository) UpdateLease(ctx context.Context, lease application.OperationLeaseRecord) (bool, error) {
	affected, err := r.queries.UpdateOperationLeaseCursor(ctx, dbsqlite.UpdateOperationLeaseCursorParams{
		CursorJson: lease.Cursor, ExpiresAt: formatTime(lease.ExpiresAt), UpdatedAt: formatTime(lease.UpdatedAt),
		LeaseKey: lease.Key, TokenHash: lease.TokenHash,
	})
	return affected == 1, repositoryError("update operation lease", err)
}

func (r *retentionRepository) ReleaseLease(ctx context.Context, key string, token []byte) (bool, error) {
	affected, err := r.queries.ReleaseOperationLease(ctx, dbsqlite.ReleaseOperationLeaseParams{LeaseKey: key, TokenHash: token})
	return affected == 1, repositoryError("release operation lease", err)
}

func (r *retentionRepository) ListAggregationResults(ctx context.Context, start, end, after time.Time, afterID string, limit int) ([]application.AggregationResultRecord, error) {
	records, err := r.queries.ListProbeResultsForDailyAggregation(ctx, dbsqlite.ListProbeResultsForDailyAggregationParams{
		StartsAt: formatTime(start), EndsAt: formatTime(end), AfterAt: formatTime(after),
		AfterID: afterID, RowLimit: int64(limit),
	})
	if err != nil {
		return nil, repositoryError("list aggregation results", err)
	}
	result := make([]application.AggregationResultRecord, 0, len(records))
	for _, record := range records {
		receivedAt, err := parseTime(record.ReceivedAt)
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
	err := r.queries.UpsertDailyUptime(ctx, dbsqlite.UpsertDailyUptimeParams{
		MonitorID: string(record.MonitorID), Day: record.Day.UTC().Format(time.DateOnly),
		PassingCount: int64(record.Passing), FailingCount: int64(record.Failing),
		UnknownCount: int64(record.Unknown), ObservedMs: record.Observed.Milliseconds(),
		UpdatedAt: formatTime(record.UpdatedAt),
	})
	return repositoryError("upsert daily uptime", err)
}

func (r *retentionRepository) ListDailyUptime(ctx context.Context, monitorID domain.MonitorID, start, end time.Time) ([]application.DailyUptimeRecord, error) {
	records, err := r.queries.ListDailyUptime(ctx, dbsqlite.ListDailyUptimeParams{
		MonitorID: string(monitorID), StartsOn: start.UTC().Format(time.DateOnly), EndsOn: end.UTC().Format(time.DateOnly),
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
	count, err := r.queries.DeleteExpiredProbeResults(ctx, dbsqlite.DeleteExpiredProbeResultsParams{Cutoff: formatTime(cutoff), RowLimit: int64(limit)})
	return count, repositoryError("delete expired probe results", err)
}

func (r *retentionRepository) DeleteExpiredDailyUptime(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	count, err := r.queries.DeleteExpiredDailyUptime(ctx, dbsqlite.DeleteExpiredDailyUptimeParams{CutoffDay: cutoff.UTC().Format(time.DateOnly), RowLimit: int64(limit)})
	return count, repositoryError("delete expired daily uptime", err)
}

func mapNotificationChannel(record dbsqlite.NotificationChannel) (application.NotificationChannelRecord, error) {
	if record.KeyVersion <= 0 || record.KeyVersion > math.MaxUint32 {
		return application.NotificationChannelRecord{}, errors.New("map notification channel: key version out of range")
	}
	createdAt, err := parseTime(record.CreatedAt)
	if err != nil {
		return application.NotificationChannelRecord{}, fmt.Errorf("map notification channel creation: %w", err)
	}
	channel, err := domain.NewNotificationChannel(domain.NotificationChannelID(record.ID), record.Name, domain.NotificationChannelKind(record.Kind), record.Enabled == 1, createdAt)
	if err != nil {
		return application.NotificationChannelRecord{}, fmt.Errorf("map notification channel: %w", err)
	}
	channel.UpdatedAt, err = parseTime(record.UpdatedAt)
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

func sqliteRouteCreateParams(route domain.NotificationRoute) (dbsqlite.CreateNotificationRouteParams, error) {
	encoded, err := encodeRoute(route)
	if err != nil {
		return dbsqlite.CreateNotificationRouteParams{}, err
	}
	return dbsqlite.CreateNotificationRouteParams{
		ID: string(route.ID), Name: route.Name, ChannelID: string(route.ChannelID), MonitorID: nullableMonitorID(route.MonitorID),
		LabelMatchersJson: encoded.labels, ActionsJson: encoded.actions, SeveritiesJson: encoded.severities,
		Template: route.Template, Enabled: boolInt(route.Enabled), Precedence: int64(route.Precedence),
		CreatedAt: formatTime(route.CreatedAt), UpdatedAt: formatTime(route.UpdatedAt),
	}, nil
}

func mapNotificationRoute(record dbsqlite.NotificationRoute) (domain.NotificationRoute, error) {
	if record.Precedence > math.MaxInt32 {
		return domain.NotificationRoute{}, errors.New("map notification route: precedence out of range")
	}
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
	createdAt, err := parseTime(record.CreatedAt)
	if err != nil {
		return domain.NotificationRoute{}, err
	}
	updatedAt, err := parseTime(record.UpdatedAt)
	if err != nil {
		return domain.NotificationRoute{}, err
	}
	route := domain.NotificationRoute{
		ID: domain.NotificationRouteID(record.ID), Name: record.Name,
		ChannelID: domain.NotificationChannelID(record.ChannelID), LabelMatchers: labels,
		Actions: actions, Severities: severities, Template: record.Template,
		Enabled: record.Enabled == 1, Precedence: int32(record.Precedence), CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	if record.MonitorID.Valid {
		monitorID := domain.MonitorID(record.MonitorID.String)
		route.MonitorID = &monitorID
	}
	return domain.NewNotificationRoute(route)
}

func mapNotificationRoutes(records []dbsqlite.NotificationRoute) ([]domain.NotificationRoute, error) {
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

func mapNotificationOutbox(record dbsqlite.NotificationOutbox) (application.NotificationOutboxRecord, error) {
	if record.AttemptCount < 0 || record.AttemptCount > math.MaxUint32 {
		return application.NotificationOutboxRecord{}, errors.New("map notification outbox: attempt count out of range")
	}
	var snapshot domain.RenderSnapshot
	if err := json.Unmarshal(record.RenderSnapshotJson, &snapshot); err != nil {
		return application.NotificationOutboxRecord{}, fmt.Errorf("map notification snapshot: %w", err)
	}
	availableAt, err := parseTime(record.AvailableAt)
	if err != nil {
		return application.NotificationOutboxRecord{}, err
	}
	createdAt, err := parseTime(record.CreatedAt)
	if err != nil {
		return application.NotificationOutboxRecord{}, err
	}
	updatedAt, err := parseTime(record.UpdatedAt)
	if err != nil {
		return application.NotificationOutboxRecord{}, err
	}
	claimExpiresAt, err := parseNullableTime(record.ClaimExpiresAt)
	if err != nil {
		return application.NotificationOutboxRecord{}, err
	}
	deliveredAt, err := parseNullableTime(record.DeliveredAt)
	if err != nil {
		return application.NotificationOutboxRecord{}, err
	}
	suppressedAt, err := parseNullableTime(record.SuppressedAt)
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

func mapNotificationAttempt(record dbsqlite.NotificationDeliveryAttempt) (application.NotificationDeliveryAttemptRecord, error) {
	if record.Ordinal <= 0 || record.Ordinal > math.MaxUint32 {
		return application.NotificationDeliveryAttemptRecord{}, errors.New("map notification attempt: ordinal out of range")
	}
	startedAt, err := parseTime(record.StartedAt)
	if err != nil {
		return application.NotificationDeliveryAttemptRecord{}, err
	}
	finishedAt, err := parseTime(record.FinishedAt)
	if err != nil {
		return application.NotificationDeliveryAttemptRecord{}, err
	}
	return application.NotificationDeliveryAttemptRecord{
		ID: record.ID, OutboxID: domain.NotificationDeliveryID(record.OutboxID), Ordinal: uint32(record.Ordinal),
		StartedAt: startedAt, FinishedAt: finishedAt, Outcome: application.NotificationAttemptOutcome(record.Outcome),
		ErrorClass: record.ErrorClass.String, Diagnostic: record.Diagnostic.String, ProviderReceipt: record.ProviderReceipt.String,
	}, nil
}

func mapMaintenance(record dbsqlite.MaintenanceInterval) (application.MaintenanceRecord, error) {
	startsAt, err := parseTime(record.StartsAt)
	if err != nil {
		return application.MaintenanceRecord{}, err
	}
	endsAt, err := parseNullableTime(record.EndsAt)
	if err != nil {
		return application.MaintenanceRecord{}, err
	}
	interval, err := domain.NewMaintenanceInterval(domain.MaintenanceID(record.ID), domain.MonitorID(record.MonitorID), startsAt, endsAt, record.Reason)
	if err != nil {
		return application.MaintenanceRecord{}, err
	}
	interval.CreatedAt, err = parseTime(record.CreatedAt)
	if err != nil {
		return application.MaintenanceRecord{}, err
	}
	claimExpiresAt, err := parseNullableTime(record.EndClaimExpiresAt)
	if err != nil {
		return application.MaintenanceRecord{}, err
	}
	processedAt, err := parseNullableTime(record.EndedNotificationSentAt)
	if err != nil {
		return application.MaintenanceRecord{}, err
	}
	updatedAt, err := parseTime(record.UpdatedAt)
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

func mapMaintenances(records []dbsqlite.MaintenanceInterval) ([]application.MaintenanceRecord, error) {
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

func mapOperationLease(record dbsqlite.OperationLease) (application.OperationLeaseRecord, error) {
	expiresAt, err := parseTime(record.ExpiresAt)
	if err != nil {
		return application.OperationLeaseRecord{}, err
	}
	updatedAt, err := parseTime(record.UpdatedAt)
	if err != nil {
		return application.OperationLeaseRecord{}, err
	}
	return application.OperationLeaseRecord{
		Key: record.LeaseKey, Owner: record.Owner, TokenHash: append([]byte(nil), record.TokenHash...),
		ExpiresAt: expiresAt, Cursor: append([]byte(nil), record.CursorJson...), UpdatedAt: updatedAt,
	}, nil
}

func mapDailyUptime(record dbsqlite.DailyUptime) (application.DailyUptimeRecord, error) {
	if record.PassingCount < 0 || record.FailingCount < 0 || record.UnknownCount < 0 || record.ObservedMs < 0 {
		return application.DailyUptimeRecord{}, errors.New("map daily uptime: negative value")
	}
	day, err := time.Parse(time.DateOnly, record.Day)
	if err != nil {
		return application.DailyUptimeRecord{}, fmt.Errorf("map daily uptime day: %w", err)
	}
	updatedAt, err := parseTime(record.UpdatedAt)
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
