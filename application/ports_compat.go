package application

import "github.com/araihu/xisnove/application/port"

// Compatibility aliases keep the current self-hosted implementation buildable
// while application services migrate to the public port package. New code
// should name application/port types directly.
var ErrNotFound = port.ErrNotFound
var ErrConflict = port.ErrConflict

type Store = port.Store
type UnitOfWork = port.UnitOfWork
type Repositories = port.Repositories
type AdminRecord = port.AdminRecord
type SessionRecord = port.SessionRecord
type MonitorLocation = port.MonitorLocation
type EnrollmentTokenRecord = port.EnrollmentTokenRecord
type AgentRecord = port.AgentRecord
type DueMonitor = port.DueMonitor
type NewRunRecord = port.NewRunRecord
type ClaimRunParams = port.ClaimRunParams
type RunRecord = port.RunRecord
type ProbeResultRecord = port.ProbeResultRecord
type ProtocolTimings = port.ProtocolTimings
type NotificationChannelRecord = port.NotificationChannelRecord
type NotificationOutboxRecord = port.NotificationOutboxRecord
type ClaimNotificationParams = port.ClaimNotificationParams
type NotificationAttemptOutcome = port.NotificationAttemptOutcome
type NotificationDeliveryAttemptRecord = port.NotificationDeliveryAttemptRecord
type FinalizeNotificationParams = port.FinalizeNotificationParams
type AuditEventRecord = port.AuditEventRecord
type MaintenanceRecord = port.MaintenanceRecord
type ClaimMaintenanceParams = port.ClaimMaintenanceParams
type DailyUptimeRecord = port.DailyUptimeRecord
type AggregationResultRecord = port.AggregationResultRecord
type OperationLeaseRecord = port.OperationLeaseRecord
type AdminRepository = port.AdminRepository
type SessionRepository = port.SessionRepository
type LocationRepository = port.LocationRepository
type MonitorRepository = port.MonitorRepository
type HealthRepository = port.HealthRepository
type AgentRepository = port.AgentRepository
type RunRepository = port.RunRepository
type ResultRepository = port.ResultRepository
type IncidentRepository = port.IncidentRepository
type NotificationChannelRepository = port.NotificationChannelRepository
type NotificationRouteRepository = port.NotificationRouteRepository
type NotificationOutboxRepository = port.NotificationOutboxRepository
type MaintenanceRepository = port.MaintenanceRepository
type AuditRepository = port.AuditRepository
type RetentionRepository = port.RetentionRepository

const (
	NotificationAttemptDelivered        = port.NotificationAttemptDelivered
	NotificationAttemptTransientFailure = port.NotificationAttemptTransientFailure
	NotificationAttemptPermanentFailure = port.NotificationAttemptPermanentFailure
	NotificationAttemptAbandoned        = port.NotificationAttemptAbandoned
)
