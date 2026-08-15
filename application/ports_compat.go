package application

import "github.com/araihu/xisnove/application/port"

// Compatibility aliases keep the current self-hosted implementation buildable
// while application services migrate to the public port package. New code
// should name application/port types directly.
var ErrNotFound = port.ErrNotFound
var ErrConflict = port.ErrConflict
var ErrInvalidScopes = port.ErrInvalidScopes

type Store = port.Store
type UnitOfWork = port.UnitOfWork
type Repositories = port.Repositories
type AdminRecord = port.AdminRecord
type SessionRecord = port.SessionRecord
type Scope = port.Scope
type PageRequest = port.PageRequest
type Page[T any] = port.Page[T]
type APITokenRecord = port.APITokenRecord
type IdempotencyRecord = port.IdempotencyRecord
type MonitorLocation = port.MonitorLocation
type EnrollmentTokenRecord = port.EnrollmentTokenRecord
type AgentRecord = port.AgentRecord
type DueMonitor = port.DueMonitor
type NewRunRecord = port.NewRunRecord
type ClaimRunParams = port.ClaimRunParams
type RunRecord = port.RunRecord
type ProbeResultRecord = port.ProbeResultRecord
type ProbeHistoryRecord = port.ProbeHistoryRecord
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
type MonitorRecord = port.MonitorRecord
type StringKeysetRequest = port.StringKeysetRequest
type IntKeysetRequest = port.IntKeysetRequest
type TimeKeysetRequest = port.TimeKeysetRequest
type IncidentListRequest = port.IncidentListRequest
type IncidentResolutionFilter = port.IncidentResolutionFilter
type SearchRequest = port.SearchRequest
type SearchResourceType = port.SearchResourceType
type SearchResult = port.SearchResult
type AgentCredentialRecord = port.AgentCredentialRecord
type CreateAgentCredentialGenerationCommand = port.CreateAgentCredentialGenerationCommand
type CredentialGenerationRevokeOutcome = port.CredentialGenerationRevokeOutcome
type ExternalOwner = port.ExternalOwner
type OperatorBinding = port.OperatorBinding
type AdminRepository = port.AdminRepository
type SessionRepository = port.SessionRepository
type APITokenRepository = port.APITokenRepository
type IdempotencyRepository = port.IdempotencyRepository
type LocationRepository = port.LocationRepository
type MonitorRepository = port.MonitorRepository
type HealthRepository = port.HealthRepository
type AgentRepository = port.AgentRepository
type RunRepository = port.RunRepository
type ResultRepository = port.ResultRepository
type StateTickRepository = port.StateTickRepository
type StateTickWriter = port.StateTickWriter
type IncidentRepository = port.IncidentRepository
type NotificationChannelRepository = port.NotificationChannelRepository
type NotificationRouteRepository = port.NotificationRouteRepository
type NotificationOutboxRepository = port.NotificationOutboxRepository
type MaintenanceRepository = port.MaintenanceRepository
type AuditRepository = port.AuditRepository
type AuditSubjectReader = port.AuditSubjectReader
type RetentionRepository = port.RetentionRepository
type ManagementQueryRepository = port.ManagementQueryRepository
type ManagementCommandRepository = port.ManagementCommandRepository
type OperatorRepository = port.OperatorRepository

var ErrRetryableTransaction = port.ErrRetryableTransaction

const (
	DefaultPageLimit                          = port.DefaultPageLimit
	MaxPageLimit                              = port.MaxPageLimit
	ScopeTokensRead                           = port.ScopeTokensRead
	ScopeTokensWrite                          = port.ScopeTokensWrite
	ScopeLocationsRead                        = port.ScopeLocationsRead
	ScopeLocationsWrite                       = port.ScopeLocationsWrite
	ScopeMonitorsRead                         = port.ScopeMonitorsRead
	ScopeMonitorsWrite                        = port.ScopeMonitorsWrite
	ScopeAgentsRead                           = port.ScopeAgentsRead
	ScopeAgentsWrite                          = port.ScopeAgentsWrite
	ScopeIncidentsRead                        = port.ScopeIncidentsRead
	ScopeNotificationsRead                    = port.ScopeNotificationsRead
	ScopeNotificationsWrite                   = port.ScopeNotificationsWrite
	ScopeMaintenanceRead                      = port.ScopeMaintenanceRead
	ScopeMaintenanceWrite                     = port.ScopeMaintenanceWrite
	ScopeDiscoveryRead                        = port.ScopeDiscoveryRead
	ScopeDiscoveryWrite                       = port.ScopeDiscoveryWrite
	ScopeStatusRead                           = port.ScopeStatusRead
	ScopeOperatorProvision                    = port.ScopeOperatorProvision
	NotificationAttemptDelivered              = port.NotificationAttemptDelivered
	NotificationAttemptTransientFailure       = port.NotificationAttemptTransientFailure
	NotificationAttemptPermanentFailure       = port.NotificationAttemptPermanentFailure
	NotificationAttemptAbandoned              = port.NotificationAttemptAbandoned
	IncidentResolutionAll                     = port.IncidentResolutionAll
	IncidentResolutionOpen                    = port.IncidentResolutionOpen
	IncidentResolutionResolved                = port.IncidentResolutionResolved
	SearchResourceMonitor                     = port.SearchResourceMonitor
	CredentialGenerationRevoked               = port.CredentialGenerationRevoked
	CredentialGenerationAlreadyRevoked        = port.CredentialGenerationAlreadyRevoked
	CredentialGenerationNotFound              = port.CredentialGenerationNotFound
	CredentialGenerationCurrent               = port.CredentialGenerationCurrent
	CredentialGenerationReplacementUnobserved = port.CredentialGenerationReplacementUnobserved
)

func NormalizePageLimit(limit int) int {
	return port.NormalizePageLimit(limit)
}
