package port

import (
	"context"
	"time"

	"github.com/araihu/xisnove/domain"
)

type NotificationChannelRecord struct {
	Channel         domain.NotificationChannel
	EncryptedConfig []byte
	KeyVersion      uint32
}

type NotificationOutboxRecord struct {
	ID              domain.NotificationDeliveryID
	IncidentEventID string
	RouteID         domain.NotificationRouteID
	ChannelID       domain.NotificationChannelID
	DedupeKey       string
	RenderSnapshot  domain.RenderSnapshot
	State           domain.DeliveryState
	AvailableAt     time.Time
	ClaimOwner      string
	ClaimTokenHash  []byte
	ClaimExpiresAt  *time.Time
	AttemptCount    uint32
	LastErrorClass  string
	LastDiagnostic  string
	DeliveredAt     *time.Time
	SuppressedAt    *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ClaimNotificationParams struct {
	Owner          string
	ClaimTokenHash []byte
	ClaimExpiresAt time.Time
	Now            time.Time
}

type NotificationAttemptOutcome string

const (
	NotificationAttemptDelivered        NotificationAttemptOutcome = "delivered"
	NotificationAttemptTransientFailure NotificationAttemptOutcome = "transient-failure"
	NotificationAttemptPermanentFailure NotificationAttemptOutcome = "permanent-failure"
	NotificationAttemptAbandoned        NotificationAttemptOutcome = "abandoned"
)

type NotificationDeliveryAttemptRecord struct {
	ID              string
	OutboxID        domain.NotificationDeliveryID
	Ordinal         uint32
	StartedAt       time.Time
	FinishedAt      time.Time
	Outcome         NotificationAttemptOutcome
	ErrorClass      string
	Diagnostic      string
	ProviderReceipt string
}

type FinalizeNotificationParams struct {
	ID             domain.NotificationDeliveryID
	ClaimTokenHash []byte
	At             time.Time
	AvailableAt    time.Time
	ErrorClass     string
	Diagnostic     string
}

type AuditEventRecord struct {
	ID          string
	Kind        string
	SubjectKind string
	SubjectID   string
	IncidentID  *domain.IncidentID
	Payload     []byte
	CreatedAt   time.Time
}

type MaintenanceRecord struct {
	Interval                domain.MaintenanceInterval
	EndClaimOwner           string
	EndClaimTokenHash       []byte
	EndClaimExpiresAt       *time.Time
	EndedNotificationSentAt *time.Time
	UpdatedAt               time.Time
}

type ClaimMaintenanceParams struct {
	Owner          string
	ClaimTokenHash []byte
	ClaimExpiresAt time.Time
	Now            time.Time
}

type DailyUptimeRecord struct {
	MonitorID domain.MonitorID
	Day       time.Time
	Passing   uint64
	Failing   uint64
	Unknown   uint64
	Observed  time.Duration
	UpdatedAt time.Time
}

type AggregationResultRecord struct {
	ID         string
	MonitorID  domain.MonitorID
	ReceivedAt time.Time
	Passed     bool
	Latency    time.Duration
}

type OperationLeaseRecord struct {
	Key       string
	Owner     string
	TokenHash []byte
	ExpiresAt time.Time
	Cursor    []byte
	UpdatedAt time.Time
}

type NotificationChannelRepository interface {
	Create(context.Context, NotificationChannelRecord) error
	Get(context.Context, domain.NotificationChannelID) (NotificationChannelRecord, error)
	List(context.Context, int, int) ([]NotificationChannelRecord, error)
	Update(context.Context, NotificationChannelRecord) (bool, error)
	SetEnabled(context.Context, domain.NotificationChannelID, bool, time.Time) (bool, error)
	ListKeyVersions(context.Context) ([]uint32, error)
	ListNeedingKeyVersion(context.Context, uint32, int) ([]NotificationChannelRecord, error)
}

type NotificationRouteRepository interface {
	Create(context.Context, domain.NotificationRoute) error
	Get(context.Context, domain.NotificationRouteID) (domain.NotificationRoute, error)
	List(context.Context, int, int) ([]domain.NotificationRoute, error)
	ListEnabled(context.Context) ([]domain.NotificationRoute, error)
	Update(context.Context, domain.NotificationRoute) (bool, error)
	SetEnabled(context.Context, domain.NotificationRouteID, bool, time.Time) (bool, error)
}

type NotificationOutboxRepository interface {
	Insert(context.Context, NotificationOutboxRecord) (bool, error)
	Get(context.Context, domain.NotificationDeliveryID) (NotificationOutboxRecord, error)
	List(context.Context, int, int) ([]NotificationOutboxRecord, error)
	ClaimDue(context.Context, ClaimNotificationParams) (NotificationOutboxRecord, error)
	AppendAttempt(context.Context, NotificationDeliveryAttemptRecord) error
	ListAttempts(context.Context, domain.NotificationDeliveryID) ([]NotificationDeliveryAttemptRecord, error)
	MarkDelivered(context.Context, FinalizeNotificationParams) (bool, error)
	MarkRetrying(context.Context, FinalizeNotificationParams) (bool, error)
	MarkPermanentFailure(context.Context, FinalizeNotificationParams) (bool, error)
	MarkSuppressed(context.Context, FinalizeNotificationParams) (bool, error)
	ReleaseClaim(context.Context, FinalizeNotificationParams) (bool, error)
	Replay(context.Context, domain.NotificationDeliveryID, time.Time) (bool, error)
}

type MaintenanceRepository interface {
	Create(context.Context, MaintenanceRecord) error
	Get(context.Context, domain.MaintenanceID) (MaintenanceRecord, error)
	List(context.Context, int, int) ([]MaintenanceRecord, error)
	ListActive(context.Context, domain.MonitorID, time.Time) ([]MaintenanceRecord, error)
	End(context.Context, domain.MaintenanceID, time.Time) (bool, error)
	DeleteFuture(context.Context, domain.MaintenanceID, time.Time) (bool, error)
	ClaimEnded(context.Context, ClaimMaintenanceParams) (MaintenanceRecord, error)
	MarkEndedProcessed(context.Context, domain.MaintenanceID, []byte, time.Time) (bool, error)
	ReleaseEndedClaim(context.Context, domain.MaintenanceID, []byte, time.Time) (bool, error)
}

type AuditRepository interface {
	Append(context.Context, AuditEventRecord) error
	ListByIncident(context.Context, domain.IncidentID) ([]AuditEventRecord, error)
}

type RetentionRepository interface {
	ClaimLease(context.Context, OperationLeaseRecord, time.Time) (OperationLeaseRecord, error)
	UpdateLease(context.Context, OperationLeaseRecord) (bool, error)
	ReleaseLease(context.Context, string, []byte) (bool, error)
	ListAggregationResults(context.Context, time.Time, time.Time, time.Time, string, int) ([]AggregationResultRecord, error)
	UpsertDailyUptime(context.Context, DailyUptimeRecord) error
	ListDailyUptime(context.Context, domain.MonitorID, time.Time, time.Time) ([]DailyUptimeRecord, error)
	DeleteExpiredResults(context.Context, time.Time, int) (int64, error)
	DeleteExpiredDailyUptime(context.Context, time.Time, int) (int64, error)
}
