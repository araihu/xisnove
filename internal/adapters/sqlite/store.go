package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	dbsqlite "github.com/araihu/xisnove/db/generated/sqlite"
	"github.com/araihu/xisnove/internal/application"
	"github.com/araihu/xisnove/internal/domain"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) application.Store {
	return &store{db: db}
}

func (s *store) Repositories() application.Repositories {
	return newRepositories(dbsqlite.New(s.db))
}

func (s *store) WithinTx(
	ctx context.Context,
	fn func(application.Repositories) error,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := fn(newRepositories(dbsqlite.New(s.db).WithTx(tx))); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return nil
}

func newRepositories(queries *dbsqlite.Queries) application.Repositories {
	return application.Repositories{
		Admins:    &adminRepository{queries: queries},
		Sessions:  &sessionRepository{queries: queries},
		Locations: &locationRepository{queries: queries},
		Monitors:  &monitorRepository{queries: queries},
		Health:    &healthRepository{queries: queries},
		Agents:    &agentRepository{queries: queries},
	}
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
	httpJSON, err := json.Marshal(monitor.HTTP)
	if err != nil {
		return fmt.Errorf("encode HTTP probe: %w", err)
	}
	err = r.queries.CreateMonitor(ctx, dbsqlite.CreateMonitorParams{
		ID:                string(monitor.ID),
		Name:              monitor.Name,
		Kind:              string(monitor.Kind),
		IntervalMs:        monitor.Interval.Milliseconds(),
		TimeoutMs:         monitor.Timeout.Milliseconds(),
		FailureThreshold:  int64(monitor.FailureThreshold),
		RecoveryThreshold: int64(monitor.RecoveryThreshold),
		HttpJson:          httpJSON,
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

type healthRepository struct {
	queries *dbsqlite.Queries
}

type agentRepository struct {
	queries *dbsqlite.Queries
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
	})
	if err != nil {
		return repositoryError("create agent", err)
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
	return mapAgent(record)
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
	affected, err := r.queries.UpdateAgentHeartbeat(
		ctx,
		dbsqlite.UpdateAgentHeartbeatParams{
			Version:              nullableString(version),
			CredentialGeneration: int64(credentialGeneration),
			CapabilitiesJson:     capabilitiesJSON,
			LastSeenAt:           nullableTimeValue(lastSeenAt),
			ID:                   string(agentID),
		},
	)
	if err != nil {
		return false, repositoryError("update agent heartbeat", err)
	}
	return affected == 1, nil
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
		)
		if err != nil {
			return nil, err
		}
		health = append(health, item)
	}
	return health, nil
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
		record.RecoveryThreshold > math.MaxUint16 {
		return domain.Monitor{}, errors.New("map monitor: threshold exceeds uint16")
	}
	var probe domain.HTTPProbe
	if err := json.Unmarshal(record.HttpJson, &probe); err != nil {
		return domain.Monitor{}, fmt.Errorf("decode HTTP probe: %w", err)
	}
	createdAt, err := parseTime(record.CreatedAt)
	if err != nil {
		return domain.Monitor{}, fmt.Errorf("map monitor creation: %w", err)
	}
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID:                domain.MonitorID(record.ID),
		Name:              record.Name,
		Interval:          time.Duration(record.IntervalMs) * time.Millisecond,
		Timeout:           time.Duration(record.TimeoutMs) * time.Millisecond,
		FailureThreshold:  uint16(record.FailureThreshold),
		RecoveryThreshold: uint16(record.RecoveryThreshold),
		HTTP:              probe,
		CreatedAt:         createdAt,
	})
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

func repositoryError(operation string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, application.ErrNotFound)
	}
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) &&
		sqliteErr.Code()&0xff == sqlite3.SQLITE_CONSTRAINT {
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
