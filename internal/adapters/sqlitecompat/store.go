package sqlitecompat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	application "github.com/araihu/xisnove/application/port"
	dbsqlite "github.com/araihu/xisnove/db/generated/sqlite"
	"github.com/araihu/xisnove/domain"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
	turso "turso.tech/database/tursogo"
)

type store struct {
	db *sql.DB
}

func classifyTransactionError(err error) error {
	if err == nil || errors.Is(err, application.ErrRetryableTransaction) {
		return err
	}
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() & 0xff {
		case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
			return fmt.Errorf("%w: %w", application.ErrRetryableTransaction, err)
		}
	}
	if errors.Is(err, turso.ErrTursoBusy) || hasCanonicalTursoStaleSnapshot(err) {
		return fmt.Errorf("%w: %w", application.ErrRetryableTransaction, err)
	}
	return err
}

func hasCanonicalTursoStaleSnapshot(err error) bool {
	const message = "turso: error: database snapshot is stale, rollback and retry the transaction"
	for current := err; current != nil; current = errors.Unwrap(current) {
		if current.Error() == message {
			return true
		}
	}
	return false
}

func NewStore(db *sql.DB) application.Store {
	return &store{db: db}
}

func (s *store) Repositories() application.Repositories {
	return newRepositories(dbsqlite.New(s.db))
}

func (s *store) View(
	ctx context.Context,
	fn func(context.Context, application.Repositories) error,
) error {
	return fn(ctx, s.Repositories())
}

func (s *store) Transact(
	ctx context.Context,
	fn func(context.Context, application.Repositories) error,
) error {
	return s.transact(ctx, func(repositories application.Repositories) error {
		return fn(ctx, repositories)
	})
}

func (s *store) WithinTx(
	ctx context.Context,
	fn func(application.Repositories) error,
) error {
	return s.transact(ctx, fn)
}

func (s *store) transact(
	ctx context.Context,
	fn func(application.Repositories) error,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", classifyTransactionError(err))
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := fn(newRepositories(dbsqlite.New(s.db).WithTx(tx))); err != nil {
		return classifyTransactionError(err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", classifyTransactionError(err))
	}
	committed = true
	return nil
}

func newRepositories(queries *dbsqlite.Queries) application.Repositories {
	return application.Repositories{
		Admins:               &adminRepository{queries: queries},
		Sessions:             &sessionRepository{queries: queries},
		APITokens:            &apiTokenRepository{queries: queries},
		Idempotency:          &idempotencyRepository{queries: queries},
		Locations:            &locationRepository{queries: queries},
		Monitors:             &monitorRepository{queries: queries},
		Health:               &healthRepository{queries: queries},
		Agents:               &agentRepository{queries: queries},
		Runs:                 &runRepository{queries: queries},
		Results:              &resultRepository{queries: queries},
		Incidents:            &incidentRepository{queries: queries},
		NotificationChannels: &notificationChannelRepository{queries: queries},
		NotificationRoutes:   &notificationRouteRepository{queries: queries},
		NotificationOutbox:   &notificationOutboxRepository{queries: queries},
		Maintenance:          &maintenanceRepository{queries: queries},
		Audit:                &auditRepository{queries: queries},
		Retention:            &retentionRepository{queries: queries},
	}
}

func (r *sessionRepository) Revoke(ctx context.Context, id string, at time.Time) (bool, error) {
	count, err := r.queries.RevokeSession(ctx, dbsqlite.RevokeSessionParams{
		RevokedAt: nullableTimeValue(at), ID: id,
	})
	if err != nil {
		return false, repositoryError("revoke session", err)
	}
	return count == 1, nil
}

type adminRepository struct {
	queries *dbsqlite.Queries
}

func (r *adminRepository) Count(ctx context.Context) (int64, error) {
	count, err := r.queries.CountAdmins(ctx)
	if err != nil {
		return 0, fmt.Errorf("count admins: %w", err)
	}
	return count, nil
}

func (r *adminRepository) Create(ctx context.Context, admin application.AdminRecord) error {
	err := r.queries.CreateAdmin(ctx, dbsqlite.CreateAdminParams{
		ID:           admin.ID,
		Email:        admin.Email,
		PasswordHash: admin.PasswordHash,
		CreatedAt:    formatTime(admin.CreatedAt),
	})
	if err != nil {
		return repositoryError("create admin", err)
	}
	return nil
}

func (r *adminRepository) FindByEmail(
	ctx context.Context,
	email string,
) (application.AdminRecord, error) {
	record, err := r.queries.FindAdminByEmail(ctx, email)
	if err != nil {
		return application.AdminRecord{}, repositoryError("find admin", err)
	}
	createdAt, err := parseTime(record.CreatedAt)
	if err != nil {
		return application.AdminRecord{}, fmt.Errorf("map admin: %w", err)
	}
	return application.AdminRecord{
		ID:           record.ID,
		Email:        record.Email,
		PasswordHash: record.PasswordHash,
		CreatedAt:    createdAt,
	}, nil
}

type sessionRepository struct {
	queries *dbsqlite.Queries
}

func (r *sessionRepository) Create(
	ctx context.Context,
	session application.SessionRecord,
) error {
	err := r.queries.CreateSession(ctx, dbsqlite.CreateSessionParams{
		ID:        session.ID,
		AdminID:   session.AdminID,
		TokenHash: session.TokenHash,
		ExpiresAt: formatTime(session.ExpiresAt),
		RevokedAt: nullableTime(session.RevokedAt),
	})
	if err != nil {
		return repositoryError("create session", err)
	}
	return nil
}

func (r *sessionRepository) FindActiveByTokenHash(
	ctx context.Context,
	tokenHash []byte,
	now time.Time,
) (application.SessionRecord, error) {
	record, err := r.queries.FindActiveSessionByTokenHash(
		ctx,
		dbsqlite.FindActiveSessionByTokenHashParams{
			TokenHash: tokenHash,
			Now:       formatTime(now),
		},
	)
	if err != nil {
		return application.SessionRecord{}, repositoryError("find active session", err)
	}

	expiresAt, err := parseTime(record.ExpiresAt)
	if err != nil {
		return application.SessionRecord{}, fmt.Errorf("map session expiry: %w", err)
	}
	revokedAt, err := parseNullableTime(record.RevokedAt)
	if err != nil {
		return application.SessionRecord{}, fmt.Errorf("map session revocation: %w", err)
	}
	return application.SessionRecord{
		ID:        record.ID,
		AdminID:   record.AdminID,
		TokenHash: record.TokenHash,
		ExpiresAt: expiresAt,
		RevokedAt: revokedAt,
	}, nil
}

type locationRepository struct {
	queries *dbsqlite.Queries
}

func (r *locationRepository) Create(ctx context.Context, location domain.Location) error {
	err := r.queries.CreateLocation(ctx, dbsqlite.CreateLocationParams{
		ID:        string(location.ID),
		Name:      location.Name,
		CreatedAt: formatTime(location.CreatedAt),
	})
	if err != nil {
		return repositoryError("create location", err)
	}
	return nil
}

func (r *locationRepository) Get(
	ctx context.Context,
	id domain.LocationID,
) (domain.Location, error) {
	record, err := r.queries.GetLocation(ctx, string(id))
	if err != nil {
		return domain.Location{}, repositoryError("get location", err)
	}
	createdAt, err := parseTime(record.CreatedAt)
	if err != nil {
		return domain.Location{}, fmt.Errorf("map location: %w", err)
	}
	return domain.Location{
		ID:        domain.LocationID(record.ID),
		Name:      record.Name,
		CreatedAt: createdAt,
	}, nil
}

type monitorRepository struct {
	queries *dbsqlite.Queries
}

func (r *monitorRepository) Create(ctx context.Context, monitor domain.Monitor) error {
	probeJSON, err := json.Marshal(monitor.Probe())
	if err != nil {
		return fmt.Errorf("encode probe definition: %w", err)
	}
	labels := monitor.MetadataLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labelsJSON, err := json.Marshal(labels)
	if err != nil {
		return fmt.Errorf("encode monitor labels: %w", err)
	}
	err = r.queries.CreateMonitor(ctx, dbsqlite.CreateMonitorParams{
		ID:                string(monitor.ID),
		Name:              monitor.Name,
		Description:       monitor.Description,
		LabelsJson:        labelsJSON,
		DisplayOrder:      int64(monitor.DisplayOrder),
		Public:            boolInt(monitor.Public),
		Kind:              string(monitor.Kind),
		IntervalMs:        monitor.Interval.Milliseconds(),
		TimeoutMs:         monitor.Timeout.Milliseconds(),
		FailureThreshold:  int64(monitor.FailureThreshold),
		RecoveryThreshold: int64(monitor.RecoveryThreshold),
		ProbeJson:         probeJSON,
		Enabled:           boolInt(monitor.Enabled),
		NextRunAt:         formatTime(monitor.NextRunAt),
		CreatedAt:         formatTime(monitor.CreatedAt),
		UpdatedAt:         formatTime(monitor.UpdatedAt),
	})
	if err != nil {
		return repositoryError("create monitor", err)
	}
	return nil
}

func (r *monitorRepository) Get(
	ctx context.Context,
	id domain.MonitorID,
) (domain.Monitor, error) {
	record, err := r.queries.GetMonitor(ctx, string(id))
	if err != nil {
		return domain.Monitor{}, repositoryError("get monitor", err)
	}
	return mapMonitor(record)
}

func (r *monitorRepository) AssignLocation(
	ctx context.Context,
	assignment application.MonitorLocation,
) error {
	err := r.queries.AssignMonitorLocation(ctx, dbsqlite.AssignMonitorLocationParams{
		MonitorID:  string(assignment.MonitorID),
		LocationID: string(assignment.LocationID),
		Required:   boolInt(assignment.Required),
	})
	if err != nil {
		return repositoryError("assign monitor location", err)
	}
	return nil
}

func (r *monitorRepository) GetAssignment(
	ctx context.Context,
	monitorID domain.MonitorID,
) (application.MonitorLocation, error) {
	record, err := r.queries.GetMonitorLocation(ctx, string(monitorID))
	if err != nil {
		return application.MonitorLocation{}, repositoryError("get monitor assignment", err)
	}
	return application.MonitorLocation{
		MonitorID:  domain.MonitorID(record.MonitorID),
		LocationID: domain.LocationID(record.LocationID),
		Required:   record.Required == 1,
	}, nil
}

func (r *monitorRepository) ListDue(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]application.DueMonitor, error) {
	records, err := r.queries.ListDueMonitorLocations(
		ctx,
		dbsqlite.ListDueMonitorLocationsParams{
			Now: formatTime(now), RowLimit: int64(limit),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("list due monitor locations: %w", err)
	}
	due := make([]application.DueMonitor, 0, len(records))
	for _, record := range records {
		monitor, err := mapMonitor(dbsqlite.Monitor{
			ID:                record.ID,
			Name:              record.Name,
			Description:       record.Description,
			LabelsJson:        record.LabelsJson,
			DisplayOrder:      record.DisplayOrder,
			Public:            record.Public,
			Kind:              record.Kind,
			IntervalMs:        record.IntervalMs,
			TimeoutMs:         record.TimeoutMs,
			FailureThreshold:  record.FailureThreshold,
			RecoveryThreshold: record.RecoveryThreshold,
			ProbeJson:         record.ProbeJson,
			Enabled:           record.Enabled,
			NextRunAt:         record.NextRunAt,
			CreatedAt:         record.CreatedAt,
			UpdatedAt:         record.UpdatedAt,
		})
		if err != nil {
			return nil, err
		}
		nextRunAt, err := parseTime(record.NextRunAt)
		if err != nil {
			return nil, fmt.Errorf("map due monitor schedule: %w", err)
		}
		due = append(due, application.DueMonitor{
			Monitor:    monitor,
			LocationID: domain.LocationID(record.LocationID),
			Required:   record.Required == 1,
			NextRunAt:  nextRunAt,
		})
	}
	return due, nil
}

func (r *monitorRepository) AdvanceNextRun(
	ctx context.Context,
	monitorID domain.MonitorID,
	nextRunAt time.Time,
	updatedAt time.Time,
) (bool, error) {
	affected, err := r.queries.AdvanceMonitorSchedule(
		ctx,
		dbsqlite.AdvanceMonitorScheduleParams{
			NextRunAt: formatTime(nextRunAt),
			UpdatedAt: formatTime(updatedAt),
			ID:        string(monitorID),
		},
	)
	if err != nil {
		return false, repositoryError("advance monitor schedule", err)
	}
	return affected == 1, nil
}

type healthRepository struct {
	queries *dbsqlite.Queries
}

type agentRepository struct {
	queries *dbsqlite.Queries
}

type runRepository struct {
	queries *dbsqlite.Queries
}

type resultRepository struct {
	queries *dbsqlite.Queries
}

type incidentRepository struct {
	queries *dbsqlite.Queries
}

func (r *runRepository) DatabaseNow(ctx context.Context) (time.Time, error) {
	value, err := r.queries.DatabaseNow(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("read database time: %w", err)
	}
	now, err := parseTime(value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse database time: %w", err)
	}
	return now, nil
}

func (r *runRepository) Insert(
	ctx context.Context,
	record application.NewRunRecord,
) (bool, error) {
	probeJSON, err := json.Marshal(record.Probe)
	if err != nil {
		return false, fmt.Errorf("encode run probe: %w", err)
	}
	affected, err := r.queries.InsertScheduledRun(
		ctx,
		dbsqlite.InsertScheduledRunParams{
			ID:           string(record.ID),
			MonitorID:    string(record.MonitorID),
			LocationID:   string(record.LocationID),
			ScheduledFor: formatTime(record.ScheduledFor),
			ProbeJson:    probeJSON,
			ProbeKind:    string(record.Probe.Kind),
			TimeoutMs:    record.Timeout.Milliseconds(),
		},
	)
	if err != nil {
		return false, repositoryError("insert scheduled run", err)
	}
	return affected == 1, nil
}

func (r *runRepository) ClaimProbe(
	ctx context.Context,
	params application.ClaimRunParams,
) (application.RunRecord, error) {
	capabilities := make([]string, len(params.Capabilities))
	for i, capability := range params.Capabilities {
		capabilities[i] = string(capability)
	}
	record, err := r.queries.ClaimProbeRun(ctx, dbsqlite.ClaimProbeRunParams{
		AgentID:        nullableString(string(params.AgentID)),
		LeaseTokenHash: params.LeaseTokenHash,
		LeaseExpiresAt: nullableTimeValue(params.LeaseExpiresAt),
		Now:            nullableTimeValue(params.Now),
		Capabilities:   capabilities,
	})
	if err != nil {
		return application.RunRecord{}, repositoryError("claim probe run", err)
	}
	return mapRun(record)
}

func (r *runRepository) Get(
	ctx context.Context,
	runID domain.CheckRunID,
) (application.RunRecord, error) {
	record, err := r.queries.GetCheckRun(ctx, string(runID))
	if err != nil {
		return application.RunRecord{}, repositoryError("get check run", err)
	}
	return mapRun(record)
}

func (r *runRepository) Resolve(
	ctx context.Context,
	runID domain.CheckRunID,
	agentID domain.AgentID,
	leaseTokenHash []byte,
	resolvedAt time.Time,
) (bool, error) {
	affected, err := r.queries.ResolveCheckRun(ctx, dbsqlite.ResolveCheckRunParams{
		ResolvedAt:     nullableTimeValue(resolvedAt),
		ID:             string(runID),
		AgentID:        nullableString(string(agentID)),
		LeaseTokenHash: leaseTokenHash,
	})
	if err != nil {
		return false, repositoryError("resolve check run", err)
	}
	return affected == 1, nil
}

func (r *resultRepository) GetByID(
	ctx context.Context,
	id string,
) (application.ProbeResultRecord, error) {
	record, err := r.queries.GetProbeResultByID(ctx, id)
	if err != nil {
		return application.ProbeResultRecord{}, repositoryError("get probe result", err)
	}
	return mapProbeResult(record)
}

func (r *resultRepository) GetByRun(
	ctx context.Context,
	runID domain.CheckRunID,
) (application.ProbeResultRecord, error) {
	record, err := r.queries.GetProbeResultByRun(ctx, string(runID))
	if err != nil {
		return application.ProbeResultRecord{}, repositoryError("get probe result by run", err)
	}
	return mapProbeResult(record)
}

func (r *resultRepository) Insert(
	ctx context.Context,
	record application.ProbeResultRecord,
) (bool, error) {
	observedValuesJSON, err := json.Marshal(record.ObservedValues)
	if err != nil {
		return false, fmt.Errorf("encode observed values: %w", err)
	}
	timingsJSON, err := json.Marshal(protocolTimingsJSON{
		DNSMillis:       record.ProtocolTimings.DNS.Milliseconds(),
		ConnectMillis:   record.ProtocolTimings.Connect.Milliseconds(),
		TLSMillis:       record.ProtocolTimings.TLS.Milliseconds(),
		FirstByteMillis: record.ProtocolTimings.FirstByte.Milliseconds(),
	})
	if err != nil {
		return false, fmt.Errorf("encode protocol timings: %w", err)
	}
	affected, err := r.queries.InsertProbeResult(ctx, dbsqlite.InsertProbeResultParams{
		ID:                  record.ID,
		RunID:               string(record.RunID),
		AgentID:             string(record.AgentID),
		StartedAt:           formatTime(record.StartedAt),
		FinishedAt:          formatTime(record.FinishedAt),
		ReceivedAt:          formatTime(record.ReceivedAt),
		Outcome:             map[bool]string{true: "passed", false: "failed"}[record.Passed],
		LatencyMs:           record.Latency.Milliseconds(),
		ObservedStatus:      nullableInt(record.ObservedStatus),
		BodyAssertionPassed: nullableBool(record.BodyAssertionPassed),
		ErrorCode:           nullableString(record.ErrorCode),
		DiagnosticSample:    nullableString(record.DiagnosticSample),
		ObservedValuesJson:  observedValuesJSON,
		TlsNotAfter:         nullableTime(record.TLSNotAfter),
		ProtocolTimingsJson: timingsJSON,
	})
	if err != nil {
		return false, repositoryError("insert probe result", err)
	}
	return affected == 1, nil
}

func (r *incidentRepository) GetActive(
	ctx context.Context,
	monitorID domain.MonitorID,
) (*domain.Incident, error) {
	record, err := r.queries.GetActiveIncidentByMonitor(ctx, string(monitorID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, repositoryError("get active incident", err)
	}
	incident, err := mapIncident(record)
	if err != nil {
		return nil, err
	}
	return &incident, nil
}

func (r *incidentRepository) Open(ctx context.Context, incident domain.Incident) error {
	err := r.queries.OpenIncident(ctx, dbsqlite.OpenIncidentParams{
		ID:               string(incident.ID),
		MonitorID:        string(incident.MonitorID),
		State:            string(incident.State),
		Severity:         string(incident.Severity),
		OpenedAt:         formatTime(incident.OpenedAt),
		LastTransitionAt: formatTime(incident.LastTransitionAt),
	})
	if err != nil {
		return repositoryError("open incident", err)
	}
	return nil
}

func (r *incidentRepository) Update(ctx context.Context, incident domain.Incident) error {
	if incident.RecoveredAt != nil {
		affected, err := r.queries.RecoverIncident(ctx, dbsqlite.RecoverIncidentParams{
			State:            string(incident.State),
			LastTransitionAt: formatTime(incident.LastTransitionAt),
			RecoveredAt:      nullableTime(incident.RecoveredAt),
			ID:               string(incident.ID),
		})
		if err != nil {
			return repositoryError("recover incident", err)
		}
		if affected != 1 {
			return application.ErrConflict
		}
		return nil
	}
	affected, err := r.queries.ChangeIncident(ctx, dbsqlite.ChangeIncidentParams{
		State:            string(incident.State),
		Severity:         string(incident.Severity),
		LastTransitionAt: formatTime(incident.LastTransitionAt),
		ID:               string(incident.ID),
	})
	if err != nil {
		return repositoryError("change incident", err)
	}
	if affected != 1 {
		return application.ErrConflict
	}
	return nil
}

func (r *incidentRepository) AppendEvent(
	ctx context.Context,
	event domain.IncidentEvent,
) error {
	err := r.queries.InsertIncidentEvent(ctx, dbsqlite.InsertIncidentEventParams{
		ID:            event.ID,
		IncidentID:    string(event.IncidentID),
		Action:        string(event.Action),
		PreviousState: nullableString(string(event.PreviousState)),
		State:         string(event.State),
		Severity:      string(event.Severity),
		CreatedAt:     formatTime(event.CreatedAt),
	})
	if err != nil {
		return repositoryError("append incident event", err)
	}
	return nil
}

func (r *agentRepository) CreateEnrollmentToken(
	ctx context.Context,
	record application.EnrollmentTokenRecord,
) error {
	err := r.queries.CreateAgentEnrollmentToken(
		ctx,
		dbsqlite.CreateAgentEnrollmentTokenParams{
			ID:         record.ID,
			LocationID: string(record.LocationID),
			TokenHash:  record.TokenHash,
			ExpiresAt:  formatTime(record.ExpiresAt),
			CreatedAt:  formatTime(record.CreatedAt),
		},
	)
	if err != nil {
		return repositoryError("create agent enrollment token", err)
	}
	return nil
}

func (r *agentRepository) ConsumeEnrollmentToken(
	ctx context.Context,
	tokenHash []byte,
	now time.Time,
	consumedAt time.Time,
) (application.EnrollmentTokenRecord, bool, error) {
	record, err := r.queries.ConsumeAgentEnrollmentToken(
		ctx,
		dbsqlite.ConsumeAgentEnrollmentTokenParams{
			ConsumedAt: nullableTimeValue(consumedAt),
			TokenHash:  tokenHash,
			Now:        formatTime(now),
		},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return application.EnrollmentTokenRecord{}, false, nil
	}
	if err != nil {
		return application.EnrollmentTokenRecord{}, false, repositoryError(
			"consume agent enrollment token",
			err,
		)
	}
	mapped, err := mapEnrollmentToken(record)
	if err != nil {
		return application.EnrollmentTokenRecord{}, false, err
	}
	return mapped, true, nil
}

func (r *agentRepository) Create(ctx context.Context, record application.AgentRecord) error {
	capabilitiesJSON, err := json.Marshal(record.Agent.Capabilities)
	if err != nil {
		return fmt.Errorf("encode agent capabilities: %w", err)
	}
	err = r.queries.CreateAgent(ctx, dbsqlite.CreateAgentParams{
		ID:                   string(record.Agent.ID),
		LocationID:           string(record.Agent.LocationID),
		Name:                 record.Agent.Name,
		CredentialHash:       record.CredentialHash,
		CredentialGeneration: int64(record.Agent.CredentialGeneration),
		CapabilitiesJson:     capabilitiesJSON,
		Version:              nullableString(record.Agent.Version),
		LastSeenAt:           nullableTimeValue(record.Agent.LastSeenAt),
		RevokedAt:            nullableTime(record.Agent.RevokedAt),
		CreatedAt:            formatTime(record.Agent.CreatedAt),
		UpdatedAt:            nullableTimeValue(record.Agent.CreatedAt),
	})
	if err != nil {
		return repositoryError("create agent", err)
	}
	err = r.queries.CreateAgentCredential(ctx, dbsqlite.CreateAgentCredentialParams{
		AgentID:        string(record.Agent.ID),
		Generation:     int64(record.Agent.CredentialGeneration),
		CredentialHash: record.CredentialHash,
		CreatedAt:      formatTime(record.Agent.CreatedAt),
		RevokedAt:      nullableTime(record.Agent.RevokedAt),
	})
	if err != nil {
		return repositoryError("create agent credential", err)
	}
	return nil
}

func (r *agentRepository) Get(
	ctx context.Context,
	agentID domain.AgentID,
) (application.AgentRecord, error) {
	record, err := r.queries.GetAgent(ctx, string(agentID))
	if err != nil {
		return application.AgentRecord{}, repositoryError("get agent", err)
	}
	return mapAgent(record)
}

func (r *agentRepository) FindActiveByCredentialHash(
	ctx context.Context,
	credentialHash []byte,
) (application.AgentRecord, error) {
	record, err := r.queries.FindActiveAgentByCredentialHash(ctx, credentialHash)
	if err != nil {
		return application.AgentRecord{}, repositoryError("find active agent", err)
	}
	mapped, err := mapAgent(dbsqlite.Agent{
		ID: record.ID, LocationID: record.LocationID, Name: record.Name,
		CredentialHash: record.CredentialHash, CredentialGeneration: record.CredentialGeneration,
		CapabilitiesJson: record.CapabilitiesJson, Version: record.Version, LastSeenAt: record.LastSeenAt,
		RevokedAt: record.RevokedAt, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	})
	if err != nil {
		return application.AgentRecord{}, err
	}
	mapped.PresentedCredentialGeneration = uint64(record.PresentedCredentialGeneration)
	return mapped, nil
}

func (r *agentRepository) UpdateHeartbeat(
	ctx context.Context,
	agentID domain.AgentID,
	credentialGeneration uint64,
	version string,
	capabilities []domain.AgentCapability,
	lastSeenAt time.Time,
) (bool, error) {
	capabilitiesJSON, err := json.Marshal(capabilities)
	if err != nil {
		return false, fmt.Errorf("encode heartbeat capabilities: %w", err)
	}
	credentialAffected, err := r.queries.TouchAgentCredentialAuthentication(
		ctx,
		dbsqlite.TouchAgentCredentialAuthenticationParams{
			LastAuthenticatedAt: nullableTimeValue(lastSeenAt),
			AgentID:             string(agentID),
			Generation:          int64(credentialGeneration),
		},
	)
	if err != nil {
		return false, repositoryError("touch agent credential authentication", err)
	}
	if credentialAffected != 1 {
		return false, nil
	}
	affected, err := r.queries.UpdateAgentHeartbeat(
		ctx,
		dbsqlite.UpdateAgentHeartbeatParams{
			Version:          nullableString(version),
			CapabilitiesJson: capabilitiesJSON,
			LastSeenAt:       nullableTimeValue(lastSeenAt),
			ID:               string(agentID),
		},
	)
	if err != nil {
		return false, repositoryError("update agent heartbeat", err)
	}
	if affected != 1 {
		return false, errors.New("active agent credential references an unavailable agent")
	}
	return true, nil
}

func (r *healthRepository) GetLocation(
	ctx context.Context,
	monitorID domain.MonitorID,
	locationID domain.LocationID,
) (domain.LocationHealth, error) {
	record, err := r.queries.GetLocationHealth(ctx, dbsqlite.GetLocationHealthParams{
		MonitorID:  string(monitorID),
		LocationID: string(locationID),
	})
	if err != nil {
		return domain.LocationHealth{}, repositoryError("get location health", err)
	}
	return mapLocationHealth(
		record.MonitorID,
		record.LocationID,
		record.State,
		record.ConsecutiveFailures,
		record.ConsecutiveSuccesses,
		record.LastObservedAt,
		record.LastTransitionAt,
		record.StaleAt,
	)
}

func (r *healthRepository) UpsertLocation(
	ctx context.Context,
	health domain.LocationHealth,
) error {
	err := r.queries.UpsertLocationHealth(ctx, dbsqlite.UpsertLocationHealthParams{
		MonitorID:            string(health.MonitorID),
		LocationID:           string(health.LocationID),
		State:                string(health.State),
		ConsecutiveFailures:  int64(health.ConsecutiveFailures),
		ConsecutiveSuccesses: int64(health.ConsecutiveSuccesses),
		LastObservedAt:       nullableTimeValue(health.LastObservedAt),
		LastTransitionAt:     nullableTimeValue(health.LastTransitionAt),
		StaleAt:              nullableTimeValue(health.StaleAt),
	})
	if err != nil {
		return fmt.Errorf("upsert location health: %w", err)
	}
	return nil
}

func (r *healthRepository) ListRequiredLocations(
	ctx context.Context,
	monitorID domain.MonitorID,
) ([]domain.LocationHealth, error) {
	records, err := r.queries.ListRequiredLocationHealth(ctx, string(monitorID))
	if err != nil {
		return nil, fmt.Errorf("list required location health: %w", err)
	}
	health := make([]domain.LocationHealth, 0, len(records))
	for _, record := range records {
		item, err := mapLocationHealth(
			record.MonitorID,
			record.LocationID,
			record.State,
			record.ConsecutiveFailures,
			record.ConsecutiveSuccesses,
			record.LastObservedAt,
			record.LastTransitionAt,
			record.StaleAt,
		)
		if err != nil {
			return nil, err
		}
		health = append(health, item)
	}
	return health, nil
}

func (r *healthRepository) ListStale(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]domain.LocationHealth, error) {
	records, err := r.queries.ListStaleLocationHealth(
		ctx,
		dbsqlite.ListStaleLocationHealthParams{
			Now: nullableTimeValue(now), RowLimit: int64(limit),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("list stale location health: %w", err)
	}
	health := make([]domain.LocationHealth, 0, len(records))
	for _, record := range records {
		item, err := mapLocationHealth(
			record.MonitorID, record.LocationID, record.State,
			record.ConsecutiveFailures, record.ConsecutiveSuccesses,
			record.LastObservedAt, record.LastTransitionAt, record.StaleAt,
		)
		if err != nil {
			return nil, err
		}
		health = append(health, item)
	}
	return health, nil
}

func (r *healthRepository) ClaimStale(
	ctx context.Context,
	monitorID domain.MonitorID,
	locationID domain.LocationID,
	staleAt time.Time,
	transitionAt time.Time,
) (bool, error) {
	affected, err := r.queries.ClaimStaleLocationHealth(
		ctx,
		dbsqlite.ClaimStaleLocationHealthParams{
			TransitionAt: nullableTimeValue(transitionAt),
			MonitorID:    string(monitorID), LocationID: string(locationID),
			StaleAt: nullableTimeValue(staleAt),
		},
	)
	if err != nil {
		return false, fmt.Errorf("claim stale location health: %w", err)
	}
	return affected == 1, nil
}

func (r *healthRepository) GetMonitor(
	ctx context.Context,
	monitorID domain.MonitorID,
) (domain.MonitorHealth, error) {
	record, err := r.queries.GetMonitorHealth(ctx, string(monitorID))
	if err != nil {
		return domain.MonitorHealth{}, repositoryError("get monitor health", err)
	}
	lastTransitionAt, err := parseNullableTime(record.LastTransitionAt)
	if err != nil {
		return domain.MonitorHealth{}, fmt.Errorf("map monitor health: %w", err)
	}
	var transition time.Time
	if lastTransitionAt != nil {
		transition = *lastTransitionAt
	}
	return domain.MonitorHealth{
		MonitorID:        domain.MonitorID(record.MonitorID),
		State:            domain.HealthState(record.State),
		LastTransitionAt: transition,
	}, nil
}

func (r *healthRepository) UpsertMonitor(
	ctx context.Context,
	health domain.MonitorHealth,
) error {
	err := r.queries.UpsertMonitorHealth(ctx, dbsqlite.UpsertMonitorHealthParams{
		MonitorID:        string(health.MonitorID),
		State:            string(health.State),
		LastTransitionAt: nullableTimeValue(health.LastTransitionAt),
	})
	if err != nil {
		return fmt.Errorf("upsert monitor health: %w", err)
	}
	return nil
}

func mapMonitor(record dbsqlite.Monitor) (domain.Monitor, error) {
	if record.FailureThreshold > math.MaxUint16 ||
		record.RecoveryThreshold > math.MaxUint16 ||
		record.DisplayOrder > math.MaxInt32 {
		return domain.Monitor{}, errors.New("map monitor: threshold exceeds uint16")
	}
	var labels map[string]string
	if err := json.Unmarshal(record.LabelsJson, &labels); err != nil {
		return domain.Monitor{}, fmt.Errorf("map monitor labels: %w", err)
	}
	probe, err := decodeProbe(domain.MonitorKind(record.Kind), record.ProbeJson)
	if err != nil {
		return domain.Monitor{}, err
	}
	createdAt, err := parseTime(record.CreatedAt)
	if err != nil {
		return domain.Monitor{}, fmt.Errorf("map monitor creation: %w", err)
	}
	monitor, err := monitorFromProbe(record, probe, createdAt, labels)
	if err != nil {
		return domain.Monitor{}, fmt.Errorf("map monitor: %w", err)
	}
	monitor.Enabled = record.Enabled == 1
	monitor.NextRunAt, err = parseTime(record.NextRunAt)
	if err != nil {
		return domain.Monitor{}, fmt.Errorf("map monitor next run: %w", err)
	}
	monitor.UpdatedAt, err = parseTime(record.UpdatedAt)
	if err != nil {
		return domain.Monitor{}, fmt.Errorf("map monitor update: %w", err)
	}
	return monitor, nil
}

func mapLocationHealth(
	monitorID string,
	locationID string,
	state string,
	consecutiveFailures int64,
	consecutiveSuccesses int64,
	lastObservedAt sql.NullString,
	lastTransitionAt sql.NullString,
	staleAt sql.NullString,
) (domain.LocationHealth, error) {
	if consecutiveFailures > math.MaxUint16 || consecutiveSuccesses > math.MaxUint16 {
		return domain.LocationHealth{}, errors.New("map location health: counter exceeds uint16")
	}
	observed, err := parseNullableTime(lastObservedAt)
	if err != nil {
		return domain.LocationHealth{}, fmt.Errorf("map location observation: %w", err)
	}
	transition, err := parseNullableTime(lastTransitionAt)
	if err != nil {
		return domain.LocationHealth{}, fmt.Errorf("map location transition: %w", err)
	}
	stale, err := parseNullableTime(staleAt)
	if err != nil {
		return domain.LocationHealth{}, fmt.Errorf("map stale deadline: %w", err)
	}
	health := domain.LocationHealth{
		MonitorID:            domain.MonitorID(monitorID),
		LocationID:           domain.LocationID(locationID),
		State:                domain.HealthState(state),
		ConsecutiveFailures:  uint16(consecutiveFailures),
		ConsecutiveSuccesses: uint16(consecutiveSuccesses),
	}
	if observed != nil {
		health.LastObservedAt = *observed
	}
	if transition != nil {
		health.LastTransitionAt = *transition
	}
	if stale != nil {
		health.StaleAt = *stale
	}
	return health, nil
}

func mapEnrollmentToken(
	record dbsqlite.AgentEnrollmentToken,
) (application.EnrollmentTokenRecord, error) {
	expiresAt, err := parseTime(record.ExpiresAt)
	if err != nil {
		return application.EnrollmentTokenRecord{}, fmt.Errorf(
			"map enrollment token expiry: %w",
			err,
		)
	}
	consumedAt, err := parseNullableTime(record.ConsumedAt)
	if err != nil {
		return application.EnrollmentTokenRecord{}, fmt.Errorf(
			"map enrollment token consumption: %w",
			err,
		)
	}
	createdAt, err := parseTime(record.CreatedAt)
	if err != nil {
		return application.EnrollmentTokenRecord{}, fmt.Errorf(
			"map enrollment token creation: %w",
			err,
		)
	}
	return application.EnrollmentTokenRecord{
		ID:         record.ID,
		LocationID: domain.LocationID(record.LocationID),
		TokenHash:  record.TokenHash,
		ExpiresAt:  expiresAt,
		ConsumedAt: consumedAt,
		CreatedAt:  createdAt,
	}, nil
}

func mapAgent(record dbsqlite.Agent) (application.AgentRecord, error) {
	if record.CredentialGeneration <= 0 {
		return application.AgentRecord{}, errors.New("map agent: invalid credential generation")
	}
	var capabilities []domain.AgentCapability
	if err := json.Unmarshal(record.CapabilitiesJson, &capabilities); err != nil {
		return application.AgentRecord{}, fmt.Errorf("decode agent capabilities: %w", err)
	}
	createdAt, err := parseTime(record.CreatedAt)
	if err != nil {
		return application.AgentRecord{}, fmt.Errorf("map agent creation: %w", err)
	}
	agent, err := domain.NewAgent(domain.NewAgentParams{
		ID:                   domain.AgentID(record.ID),
		LocationID:           domain.LocationID(record.LocationID),
		Name:                 record.Name,
		Capabilities:         capabilities,
		CredentialGeneration: uint64(record.CredentialGeneration),
		CreatedAt:            createdAt,
	})
	if err != nil {
		return application.AgentRecord{}, fmt.Errorf("map agent: %w", err)
	}
	if record.Version.Valid {
		agent.Version = record.Version.String
	}
	lastSeenAt, err := parseNullableTime(record.LastSeenAt)
	if err != nil {
		return application.AgentRecord{}, fmt.Errorf("map agent heartbeat: %w", err)
	}
	if lastSeenAt != nil {
		agent.LastSeenAt = *lastSeenAt
	}
	agent.RevokedAt, err = parseNullableTime(record.RevokedAt)
	if err != nil {
		return application.AgentRecord{}, fmt.Errorf("map agent revocation: %w", err)
	}
	return application.AgentRecord{
		Agent: agent, CredentialHash: record.CredentialHash,
	}, nil
}

func mapRun(record dbsqlite.CheckRun) (application.RunRecord, error) {
	if record.LeaseAttempt < 0 || record.LeaseAttempt > math.MaxUint32 {
		return application.RunRecord{}, errors.New("map run: lease attempt exceeds uint32")
	}
	probe, err := decodeProbe(domain.MonitorKind(record.ProbeKind), record.ProbeJson)
	if err != nil {
		return application.RunRecord{}, fmt.Errorf("decode run probe: %w", err)
	}
	scheduledFor, err := parseTime(record.ScheduledFor)
	if err != nil {
		return application.RunRecord{}, fmt.Errorf("map run schedule: %w", err)
	}
	leaseExpiresAt, err := parseNullableTime(record.LeaseExpiresAt)
	if err != nil {
		return application.RunRecord{}, fmt.Errorf("map run lease expiry: %w", err)
	}
	resolvedAt, err := parseNullableTime(record.ResolvedAt)
	if err != nil {
		return application.RunRecord{}, fmt.Errorf("map run resolution: %w", err)
	}
	run := application.RunRecord{
		ID:             domain.CheckRunID(record.ID),
		MonitorID:      domain.MonitorID(record.MonitorID),
		LocationID:     domain.LocationID(record.LocationID),
		ScheduledFor:   scheduledFor,
		Probe:          probe,
		Timeout:        time.Duration(record.TimeoutMs) * time.Millisecond,
		Status:         record.Status,
		LeaseTokenHash: record.LeaseTokenHash,
		LeaseAttempt:   uint32(record.LeaseAttempt),
		LeaseExpiresAt: leaseExpiresAt,
		ResolvedAt:     resolvedAt,
	}
	if record.LeaseAgentID.Valid {
		run.LeaseAgentID = domain.AgentID(record.LeaseAgentID.String)
	}
	return run, nil
}

func decodeProbe(kind domain.MonitorKind, data []byte) (domain.ProbeDefinition, error) {
	var probe domain.ProbeDefinition
	if err := json.Unmarshal(data, &probe); err != nil {
		return domain.ProbeDefinition{}, fmt.Errorf("decode probe definition: %w", err)
	}
	if probe.Kind == "" && kind == domain.MonitorKindHTTP {
		var legacy domain.HTTPProbe
		if err := json.Unmarshal(data, &legacy); err != nil {
			return domain.ProbeDefinition{}, fmt.Errorf("decode legacy HTTP probe: %w", err)
		}
		probe = domain.ProbeDefinition{Kind: domain.MonitorKindHTTP, HTTP: legacy}
	}
	if probe.Kind != kind {
		return domain.ProbeDefinition{}, errors.New("decode probe definition: kind mismatch")
	}
	return probe, nil
}

func monitorFromProbe(
	record dbsqlite.Monitor,
	probe domain.ProbeDefinition,
	createdAt time.Time,
	labels map[string]string,
) (domain.Monitor, error) {
	commonID := domain.MonitorID(record.ID)
	interval := time.Duration(record.IntervalMs) * time.Millisecond
	timeout := time.Duration(record.TimeoutMs) * time.Millisecond
	failures := uint16(record.FailureThreshold)
	recoveries := uint16(record.RecoveryThreshold)
	switch probe.Kind {
	case domain.MonitorKindHTTP:
		return domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
			ID: commonID, Name: record.Name, Description: record.Description,
			Labels: labels, DisplayOrder: int32(record.DisplayOrder), Public: record.Public == 1,
			Interval: interval, Timeout: timeout,
			FailureThreshold: failures, RecoveryThreshold: recoveries,
			HTTP: probe.HTTP, CreatedAt: createdAt,
		})
	case domain.MonitorKindTCP:
		return domain.NewTCPMonitor(domain.NewTCPMonitorParams{
			ID: commonID, Name: record.Name, Description: record.Description,
			Labels: labels, DisplayOrder: int32(record.DisplayOrder), Public: record.Public == 1,
			Interval: interval, Timeout: timeout,
			FailureThreshold: failures, RecoveryThreshold: recoveries,
			TCP: probe.TCP, CreatedAt: createdAt,
		})
	case domain.MonitorKindDNS:
		return domain.NewDNSMonitor(domain.NewDNSMonitorParams{
			ID: commonID, Name: record.Name, Description: record.Description,
			Labels: labels, DisplayOrder: int32(record.DisplayOrder), Public: record.Public == 1,
			Interval: interval, Timeout: timeout,
			FailureThreshold: failures, RecoveryThreshold: recoveries,
			DNS: probe.DNS, CreatedAt: createdAt,
		})
	default:
		return domain.Monitor{}, errors.New("map monitor: unsupported probe kind")
	}
}

func mapProbeResult(record dbsqlite.ProbeResult) (application.ProbeResultRecord, error) {
	startedAt, err := parseTime(record.StartedAt)
	if err != nil {
		return application.ProbeResultRecord{}, fmt.Errorf("map result start: %w", err)
	}
	finishedAt, err := parseTime(record.FinishedAt)
	if err != nil {
		return application.ProbeResultRecord{}, fmt.Errorf("map result finish: %w", err)
	}
	receivedAt, err := parseTime(record.ReceivedAt)
	if err != nil {
		return application.ProbeResultRecord{}, fmt.Errorf("map result receipt: %w", err)
	}
	var observedValues []string
	if len(record.ObservedValuesJson) != 0 {
		if err := json.Unmarshal(record.ObservedValuesJson, &observedValues); err != nil {
			return application.ProbeResultRecord{}, fmt.Errorf("map result values: %w", err)
		}
	}
	tlsNotAfter, err := parseNullableTime(record.TlsNotAfter)
	if err != nil {
		return application.ProbeResultRecord{}, fmt.Errorf("map result TLS expiry: %w", err)
	}
	var timings protocolTimingsJSON
	if len(record.ProtocolTimingsJson) != 0 {
		if err := json.Unmarshal(record.ProtocolTimingsJson, &timings); err != nil {
			return application.ProbeResultRecord{}, fmt.Errorf("map result timings: %w", err)
		}
	}
	result := application.ProbeResultRecord{
		ID:               record.ID,
		RunID:            domain.CheckRunID(record.RunID),
		AgentID:          domain.AgentID(record.AgentID),
		StartedAt:        startedAt,
		FinishedAt:       finishedAt,
		ReceivedAt:       receivedAt,
		Passed:           record.Outcome == "passed",
		Latency:          time.Duration(record.LatencyMs) * time.Millisecond,
		ErrorCode:        record.ErrorCode.String,
		DiagnosticSample: record.DiagnosticSample.String,
		ObservedValues:   observedValues,
		TLSNotAfter:      tlsNotAfter,
		ProtocolTimings: application.ProtocolTimings{
			DNS:       time.Duration(timings.DNSMillis) * time.Millisecond,
			Connect:   time.Duration(timings.ConnectMillis) * time.Millisecond,
			TLS:       time.Duration(timings.TLSMillis) * time.Millisecond,
			FirstByte: time.Duration(timings.FirstByteMillis) * time.Millisecond,
		},
	}
	if record.ObservedStatus.Valid {
		status := int(record.ObservedStatus.Int64)
		result.ObservedStatus = &status
	}
	if record.BodyAssertionPassed.Valid {
		passed := record.BodyAssertionPassed.Int64 == 1
		result.BodyAssertionPassed = &passed
	}
	return result, nil
}

type protocolTimingsJSON struct {
	DNSMillis       int64 `json:"dnsMillis,omitempty"`
	ConnectMillis   int64 `json:"connectMillis,omitempty"`
	TLSMillis       int64 `json:"tlsMillis,omitempty"`
	FirstByteMillis int64 `json:"firstByteMillis,omitempty"`
}

func mapIncident(record dbsqlite.Incident) (domain.Incident, error) {
	openedAt, err := parseTime(record.OpenedAt)
	if err != nil {
		return domain.Incident{}, fmt.Errorf("map incident opening: %w", err)
	}
	transitionAt, err := parseTime(record.LastTransitionAt)
	if err != nil {
		return domain.Incident{}, fmt.Errorf("map incident transition: %w", err)
	}
	recoveredAt, err := parseNullableTime(record.RecoveredAt)
	if err != nil {
		return domain.Incident{}, fmt.Errorf("map incident recovery: %w", err)
	}
	return domain.Incident{
		ID:               domain.IncidentID(record.ID),
		MonitorID:        domain.MonitorID(record.MonitorID),
		State:            domain.HealthState(record.State),
		Severity:         domain.IncidentSeverity(record.Severity),
		OpenedAt:         openedAt,
		LastTransitionAt: transitionAt,
		RecoveredAt:      recoveredAt,
	}, nil
}

func repositoryError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, application.ErrNotFound)
	}
	if classified := classifyTransactionError(err); errors.Is(classified, application.ErrRetryableTransaction) {
		return fmt.Errorf("%s: %w", operation, classified)
	}
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) &&
		sqliteErr.Code()&0xff == sqlite3.SQLITE_CONSTRAINT {
		return fmt.Errorf("%s: %w", operation, application.ErrConflict)
	}
	if errors.Is(err, turso.ErrTursoConstraint) {
		return fmt.Errorf("%s: %w", operation, application.ErrConflict)
	}
	// libsql-client-go currently converts the structured Hrana error into
	// errors.New(message), so no error code or public concrete type survives
	// database/sql. Keep this fallback limited to libSQL's canonical SQLite
	// constraint message instead of classifying arbitrary error strings.
	if strings.Contains("\n"+err.Error(), "\nSQLite error: ") &&
		strings.Contains(err.Error(), "constraint failed:") {
		return fmt.Errorf("%s: %w", operation, application.ErrConflict)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func nullableTime(value *time.Time) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return nullableTimeValue(*value)
}

func nullableTimeValue(value time.Time) sql.NullString {
	if value.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(value), Valid: true}
}

func nullableString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func nullableInt(value *int) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*value), Valid: true}
}

func nullableBool(value *bool) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: boolInt(*value), Valid: true}
}

func parseNullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
