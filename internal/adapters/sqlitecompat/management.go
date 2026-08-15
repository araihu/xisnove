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
)

type managementRepository struct {
	queries *dbsqlite.Queries
}

var (
	_ application.ManagementQueryRepository   = (*managementRepository)(nil)
	_ application.ManagementCommandRepository = (*managementRepository)(nil)
)

func (r *managementRepository) SearchResources(ctx context.Context, request application.SearchRequest) ([]application.SearchResult, error) {
	records, err := r.queries.ManagementSearchResources(ctx, dbsqlite.ManagementSearchResourcesParams{
		SearchQuery: request.Query, RowLimit: int64(request.Limit),
	})
	if err != nil {
		return nil, repositoryError("management search resources", err)
	}
	results := make([]application.SearchResult, 0, len(records))
	for _, record := range records {
		results = append(results, application.SearchResult{
			ResourceType: application.SearchResourceMonitor,
			ResourceID:   record.ID,
			Title:        record.Name,
			Description:  record.Description,
			Context:      strings.ToUpper(record.Kind) + " monitor",
		})
	}
	return results, nil
}

func (r *managementRepository) GetLocation(ctx context.Context, id domain.LocationID) (domain.Location, error) {
	record, err := r.queries.ManagementGetLocation(ctx, string(id))
	if err != nil {
		return domain.Location{}, repositoryError("management get location", err)
	}
	return mapManagementLocation(record.ID, record.Name, record.Enabled, record.CreatedAt, record.UpdatedAt)
}

func (r *managementRepository) ListLocations(ctx context.Context, request application.StringKeysetRequest) ([]domain.Location, error) {
	records, err := r.queries.ManagementListLocations(ctx, dbsqlite.ManagementListLocationsParams{
		HasAfter: boolInt(request.HasAfter), AfterSort: request.AfterSort,
		AfterID: request.AfterID, RowLimit: int64(request.Limit),
	})
	if err != nil {
		return nil, repositoryError("management list locations", err)
	}
	result := make([]domain.Location, 0, len(records))
	for _, record := range records {
		location, err := mapManagementLocation(record.ID, record.Name, record.Enabled, record.CreatedAt, record.UpdatedAt)
		if err != nil {
			return nil, err
		}
		result = append(result, location)
	}
	return result, nil
}

func (r *managementRepository) ReplaceLocation(ctx context.Context, location domain.Location) (bool, error) {
	count, err := r.queries.ManagementReplaceLocation(ctx, dbsqlite.ManagementReplaceLocationParams{
		ID: string(location.ID), Name: location.Name, Enabled: boolInt(location.Enabled),
		UpdatedAt: nullableTimeValue(location.UpdatedAt),
	})
	if err != nil {
		return false, repositoryError("management replace location", err)
	}
	return count == 1, nil
}

func (r *managementRepository) DisableLocation(ctx context.Context, id domain.LocationID, at time.Time) (bool, error) {
	count, err := r.queries.ManagementDisableLocation(ctx, dbsqlite.ManagementDisableLocationParams{
		ID: string(id), UpdatedAt: nullableTimeValue(at),
	})
	if err != nil {
		return false, repositoryError("management disable location", err)
	}
	return count == 1, nil
}

func (r *managementRepository) GetMonitor(ctx context.Context, id domain.MonitorID) (application.MonitorRecord, error) {
	record, err := r.queries.ManagementGetMonitor(ctx, string(id))
	if err != nil {
		return application.MonitorRecord{}, repositoryError("management get monitor", err)
	}
	return mapManagementMonitor(sqliteMonitorFromGet(record), record.LocationID, record.Required)
}

func (r *managementRepository) ListMonitors(ctx context.Context, request application.IntKeysetRequest) ([]application.MonitorRecord, error) {
	records, err := r.queries.ManagementListMonitors(ctx, dbsqlite.ManagementListMonitorsParams{
		HasAfter: boolInt(request.HasAfter), AfterSort: request.AfterSort,
		AfterID: request.AfterID, RowLimit: int64(request.Limit),
	})
	if err != nil {
		return nil, repositoryError("management list monitors", err)
	}
	result := make([]application.MonitorRecord, 0, len(records))
	for _, record := range records {
		mapped, err := mapManagementMonitor(sqliteMonitorFromList(record), record.LocationID, record.Required)
		if err != nil {
			return nil, err
		}
		result = append(result, mapped)
	}
	return result, nil
}

func (r *managementRepository) ReplaceMonitor(ctx context.Context, record application.MonitorRecord) (bool, error) {
	monitor := record.Monitor
	probeJSON, labelsJSON, err := encodeMonitorManagement(monitor)
	if err != nil {
		return false, err
	}
	count, err := r.queries.ManagementReplaceMonitor(ctx, dbsqlite.ManagementReplaceMonitorParams{
		ID: string(monitor.ID), Name: monitor.Name, Description: monitor.Description,
		LabelsJson: labelsJSON, DisplayOrder: int64(monitor.DisplayOrder), Public: boolInt(monitor.Public),
		Kind: string(monitor.Kind), IntervalMs: monitor.Interval.Milliseconds(), TimeoutMs: monitor.Timeout.Milliseconds(),
		FailureThreshold: int64(monitor.FailureThreshold), RecoveryThreshold: int64(monitor.RecoveryThreshold),
		ProbeJson: probeJSON, Enabled: boolInt(monitor.Enabled), NextRunAt: formatTime(monitor.NextRunAt),
		UpdatedAt: formatTime(monitor.UpdatedAt),
	})
	if err != nil {
		return false, repositoryError("management replace monitor", err)
	}
	if count != 1 {
		return false, nil
	}
	if err := r.queries.ManagementDeleteMonitorAssignments(ctx, string(monitor.ID)); err != nil {
		return false, repositoryError("management replace monitor assignment", err)
	}
	if err := r.queries.AssignMonitorLocation(ctx, dbsqlite.AssignMonitorLocationParams{
		MonitorID: string(monitor.ID), LocationID: string(record.LocationID), Required: boolInt(record.RequiredLocation),
	}); err != nil {
		return false, repositoryError("management replace monitor assignment", err)
	}
	return true, nil
}

func (r *managementRepository) DisableMonitor(ctx context.Context, id domain.MonitorID, at time.Time) (bool, error) {
	count, err := r.queries.ManagementDisableMonitor(ctx, dbsqlite.ManagementDisableMonitorParams{ID: string(id), UpdatedAt: formatTime(at)})
	if err != nil {
		return false, repositoryError("management disable monitor", err)
	}
	return count == 1, nil
}

func (r *managementRepository) GetAgent(ctx context.Context, id domain.AgentID) (domain.Agent, error) {
	record, err := r.queries.ManagementGetAgent(ctx, string(id))
	if err != nil {
		return domain.Agent{}, repositoryError("management get agent", err)
	}
	mapped, err := mapAgent(sqliteAgentFromGet(record))
	return mapped.Agent, err
}

func (r *managementRepository) ListAgents(ctx context.Context, request application.StringKeysetRequest) ([]domain.Agent, error) {
	records, err := r.queries.ManagementListAgents(ctx, dbsqlite.ManagementListAgentsParams{
		HasAfter: boolInt(request.HasAfter), AfterSort: request.AfterSort,
		AfterID: request.AfterID, RowLimit: int64(request.Limit),
	})
	if err != nil {
		return nil, repositoryError("management list agents", err)
	}
	result := make([]domain.Agent, 0, len(records))
	for _, record := range records {
		mapped, err := mapAgent(sqliteAgentFromList(record))
		if err != nil {
			return nil, err
		}
		result = append(result, mapped.Agent)
	}
	return result, nil
}

func (r *managementRepository) UpdateAgent(ctx context.Context, agent domain.Agent) (bool, error) {
	capabilities, err := json.Marshal(agent.Capabilities)
	if err != nil {
		return false, fmt.Errorf("encode management agent capabilities: %w", err)
	}
	count, err := r.queries.ManagementUpdateAgent(ctx, dbsqlite.ManagementUpdateAgentParams{
		ID: string(agent.ID), LocationID: string(agent.LocationID), Name: agent.Name,
		CapabilitiesJson: capabilities, UpdatedAt: nullableTimeValue(agent.UpdatedAt),
	})
	if err != nil {
		return false, repositoryError("management update agent", err)
	}
	return count == 1, nil
}

func (r *managementRepository) RevokeAgent(ctx context.Context, id domain.AgentID, at time.Time) (bool, error) {
	count, err := r.queries.ManagementRevokeAgent(ctx, dbsqlite.ManagementRevokeAgentParams{ID: string(id), RevokedAt: nullableTimeValue(at)})
	if err != nil {
		return false, repositoryError("management revoke agent", err)
	}
	if count != 1 {
		return false, nil
	}
	if err := r.queries.ManagementRevokeAllAgentCredentials(ctx, dbsqlite.ManagementRevokeAllAgentCredentialsParams{
		AgentID: string(id), RevokedAt: nullableTimeValue(at),
	}); err != nil {
		return false, repositoryError("management revoke agent credentials", err)
	}
	return true, nil
}

func (r *managementRepository) CreateAgentCredentialGeneration(ctx context.Context, command application.CreateAgentCredentialGenerationCommand) (bool, error) {
	credential := command.Credential
	if command.ExpectedCurrentGeneration == math.MaxUint64 ||
		credential.AgentID == "" || credential.Generation != command.ExpectedCurrentGeneration+1 ||
		credential.Generation > math.MaxInt64 || command.ExpectedCurrentGeneration > math.MaxInt64 {
		return false, nil
	}
	count, err := r.queries.ManagementAdvanceAgentGeneration(ctx, dbsqlite.ManagementAdvanceAgentGenerationParams{
		AgentID: string(credential.AgentID), ExpectedGeneration: int64(command.ExpectedCurrentGeneration),
		NewGeneration: int64(credential.Generation), UpdatedAt: nullableTimeValue(credential.CreatedAt),
	})
	if err != nil {
		return false, repositoryError("management advance agent generation", err)
	}
	if count != 1 {
		return false, nil
	}
	if err := r.queries.CreateAgentCredential(ctx, dbsqlite.CreateAgentCredentialParams{
		AgentID: string(credential.AgentID), Generation: int64(credential.Generation),
		CredentialHash: credential.CredentialHash, CreatedAt: formatTime(credential.CreatedAt),
		RevokedAt: nullableTime(credential.RevokedAt), LastAuthenticatedAt: nullableTime(credential.LastAuthenticatedAt),
	}); err != nil {
		return false, repositoryError("management create agent credential", err)
	}
	return true, nil
}

func (r *managementRepository) GetAgentCredentialGeneration(ctx context.Context, agentID domain.AgentID, generation uint64) (application.AgentCredentialRecord, error) {
	if generation > math.MaxInt64 {
		return application.AgentCredentialRecord{}, fmt.Errorf("management get agent credential: %w", application.ErrNotFound)
	}
	record, err := r.queries.ManagementGetAgentCredential(ctx, dbsqlite.ManagementGetAgentCredentialParams{AgentID: string(agentID), Generation: int64(generation)})
	if err != nil {
		return application.AgentCredentialRecord{}, repositoryError("management get agent credential", err)
	}
	return mapManagementCredential(record)
}

func (r *managementRepository) RevokeAgentCredentialGeneration(ctx context.Context, agentID domain.AgentID, generation uint64, at time.Time) (application.CredentialGenerationRevokeOutcome, error) {
	current, err := r.queries.ManagementGetCurrentAgentCredential(ctx, string(agentID))
	if errors.Is(repositoryError("management get current agent credential", err), application.ErrNotFound) {
		return application.CredentialGenerationNotFound, nil
	}
	if err != nil {
		return "", repositoryError("management get current agent credential", err)
	}
	target, err := r.GetAgentCredentialGeneration(ctx, agentID, generation)
	if errors.Is(err, application.ErrNotFound) {
		return application.CredentialGenerationNotFound, nil
	}
	if err != nil {
		return "", err
	}
	if target.RevokedAt != nil {
		return application.CredentialGenerationAlreadyRevoked, nil
	}
	if generation == uint64(current.Generation) {
		return application.CredentialGenerationCurrent, nil
	}
	if !current.LastAuthenticatedAt.Valid {
		return application.CredentialGenerationReplacementUnobserved, nil
	}
	count, err := r.queries.ManagementRevokeAgentCredential(ctx, dbsqlite.ManagementRevokeAgentCredentialParams{
		AgentID: string(agentID), Generation: int64(generation), RevokedAt: nullableTimeValue(at),
	})
	if err != nil {
		return "", repositoryError("management revoke agent credential", err)
	}
	if count != 1 {
		return application.CredentialGenerationAlreadyRevoked, nil
	}
	return application.CredentialGenerationRevoked, nil
}

func (r *managementRepository) GetIncident(ctx context.Context, id domain.IncidentID) (domain.Incident, error) {
	record, err := r.queries.ManagementGetIncident(ctx, string(id))
	if err != nil {
		return domain.Incident{}, repositoryError("management get incident", err)
	}
	return mapIncident(record)
}

func (r *managementRepository) ListIncidents(ctx context.Context, request application.IncidentListRequest) ([]domain.Incident, error) {
	records, err := r.queries.ManagementListIncidents(ctx, dbsqlite.ManagementListIncidentsParams{
		Resolution: string(request.Resolution), HasAfter: boolInt(request.HasAfter),
		AfterSort: formatTime(request.AfterSort), AfterID: request.AfterID, RowLimit: int64(request.Limit),
	})
	if err != nil {
		return nil, repositoryError("management list incidents", err)
	}
	result := make([]domain.Incident, 0, len(records))
	for _, record := range records {
		incident, err := mapIncident(record)
		if err != nil {
			return nil, err
		}
		result = append(result, incident)
	}
	return result, nil
}

func (r *managementRepository) ListIncidentEvents(ctx context.Context, incidentID domain.IncidentID, request application.TimeKeysetRequest) ([]domain.IncidentEvent, error) {
	records, err := r.queries.ManagementListIncidentEvents(ctx, dbsqlite.ManagementListIncidentEventsParams{
		IncidentID: string(incidentID), HasAfter: boolInt(request.HasAfter),
		AfterSort: formatTime(request.AfterSort), AfterID: request.AfterID, RowLimit: int64(request.Limit),
	})
	if err != nil {
		return nil, repositoryError("management list incident events", err)
	}
	result := make([]domain.IncidentEvent, 0, len(records))
	for _, record := range records {
		createdAt, err := parseTime(record.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("map incident event: %w", err)
		}
		previous := domain.HealthState("")
		if record.PreviousState.Valid {
			previous = domain.HealthState(record.PreviousState.String)
		}
		result = append(result, domain.IncidentEvent{ID: record.ID, IncidentID: domain.IncidentID(record.IncidentID), Action: domain.NotificationAction(record.Action), PreviousState: previous, State: domain.HealthState(record.State), Severity: domain.IncidentSeverity(record.Severity), CreatedAt: createdAt})
	}
	return result, nil
}

func mapManagementLocation(id, name string, enabled int64, created string, updated sql.NullString) (domain.Location, error) {
	createdAt, err := parseTime(created)
	if err != nil {
		return domain.Location{}, fmt.Errorf("map location creation: %w", err)
	}
	updatedAt, err := parseNullableTime(updated)
	if err != nil {
		return domain.Location{}, fmt.Errorf("map location update: %w", err)
	}
	if updatedAt == nil {
		return domain.Location{}, errors.New("map location update: missing timestamp")
	}
	return domain.Location{ID: domain.LocationID(id), Name: name, Enabled: enabled == 1, CreatedAt: createdAt, UpdatedAt: *updatedAt}, nil
}

func mapManagementCredential(record dbsqlite.AgentCredential) (application.AgentCredentialRecord, error) {
	created, err := parseTime(record.CreatedAt)
	if err != nil {
		return application.AgentCredentialRecord{}, fmt.Errorf("map agent credential creation: %w", err)
	}
	revoked, err := parseNullableTime(record.RevokedAt)
	if err != nil {
		return application.AgentCredentialRecord{}, fmt.Errorf("map agent credential revocation: %w", err)
	}
	lastAuthenticated, err := parseNullableTime(record.LastAuthenticatedAt)
	if err != nil {
		return application.AgentCredentialRecord{}, fmt.Errorf("map agent credential authentication: %w", err)
	}
	return application.AgentCredentialRecord{AgentID: domain.AgentID(record.AgentID), Generation: uint64(record.Generation), CredentialHash: append([]byte(nil), record.CredentialHash...), CreatedAt: created, RevokedAt: revoked, LastAuthenticatedAt: lastAuthenticated}, nil
}

func encodeMonitorManagement(monitor domain.Monitor) ([]byte, []byte, error) {
	probeJSON, err := json.Marshal(monitor.Probe())
	if err != nil {
		return nil, nil, fmt.Errorf("encode management monitor probe: %w", err)
	}
	labels := monitor.MetadataLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labelsJSON, err := json.Marshal(labels)
	if err != nil {
		return nil, nil, fmt.Errorf("encode management monitor labels: %w", err)
	}
	return probeJSON, labelsJSON, nil
}

func mapManagementMonitor(monitorRecord dbsqlite.Monitor, locationID string, required int64) (application.MonitorRecord, error) {
	monitor, err := mapMonitor(monitorRecord)
	if err != nil {
		return application.MonitorRecord{}, err
	}
	return application.MonitorRecord{Monitor: monitor, LocationID: domain.LocationID(locationID), RequiredLocation: required == 1}, nil
}

func sqliteMonitorFromGet(row dbsqlite.ManagementGetMonitorRow) dbsqlite.Monitor {
	return dbsqlite.Monitor{ID: row.ID, Name: row.Name, Kind: row.Kind, IntervalMs: row.IntervalMs, TimeoutMs: row.TimeoutMs, FailureThreshold: row.FailureThreshold, RecoveryThreshold: row.RecoveryThreshold, ProbeJson: row.ProbeJson, Enabled: row.Enabled, NextRunAt: row.NextRunAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Description: row.Description, LabelsJson: row.LabelsJson, DisplayOrder: row.DisplayOrder, Public: row.Public}
}

func sqliteMonitorFromList(row dbsqlite.ManagementListMonitorsRow) dbsqlite.Monitor {
	return dbsqlite.Monitor{ID: row.ID, Name: row.Name, Kind: row.Kind, IntervalMs: row.IntervalMs, TimeoutMs: row.TimeoutMs, FailureThreshold: row.FailureThreshold, RecoveryThreshold: row.RecoveryThreshold, ProbeJson: row.ProbeJson, Enabled: row.Enabled, NextRunAt: row.NextRunAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Description: row.Description, LabelsJson: row.LabelsJson, DisplayOrder: row.DisplayOrder, Public: row.Public}
}

func sqliteAgentFromGet(row dbsqlite.ManagementGetAgentRow) dbsqlite.Agent {
	return dbsqlite.Agent{ID: row.ID, LocationID: row.LocationID, Name: row.Name, CredentialGeneration: row.CredentialGeneration, CapabilitiesJson: row.CapabilitiesJson, Version: row.Version, LastSeenAt: row.LastSeenAt, RevokedAt: row.RevokedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func sqliteAgentFromList(row dbsqlite.ManagementListAgentsRow) dbsqlite.Agent {
	return dbsqlite.Agent{ID: row.ID, LocationID: row.LocationID, Name: row.Name, CredentialGeneration: row.CredentialGeneration, CapabilitiesJson: row.CapabilitiesJson, Version: row.Version, LastSeenAt: row.LastSeenAt, RevokedAt: row.RevokedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
