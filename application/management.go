package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

const (
	locationsCursorEndpoint      = "/v1/locations"
	monitorsCursorEndpoint       = "/v1/monitors"
	agentsCursorEndpoint         = "/v1/agents"
	incidentsCursorEndpoint      = "/v1/incidents"
	incidentEventsCursorEndpoint = "/v1/incidents/{incidentId}/events"
)

type ManagementServiceConfig struct {
	Store   UnitOfWork
	Cursors AudienceCursorCodec
	Tokens  TokenIssuer
	NewID   func() string
}

// ManagementService owns request normalization, opaque cursor processing, and
// public page construction. Storage adapters receive only typed keysets.
type ManagementService struct {
	store   UnitOfWork
	cursors AudienceCursorCodec
	tokens  TokenIssuer
	newID   func() string
}

func NewManagementService(config ManagementServiceConfig) *ManagementService {
	return &ManagementService{
		store: config.Store, cursors: config.Cursors, tokens: config.Tokens, newID: config.NewID,
	}
}

func (s *ManagementService) GetLocation(ctx context.Context, id domain.LocationID) (domain.Location, error) {
	var result domain.Location
	err := s.view(ctx, func(ctx context.Context, repository port.ManagementQueryRepository) error {
		var err error
		result, err = repository.GetLocation(ctx, id)
		return err
	})
	return result, wrapManagementRead("get location", err)
}

func (s *ManagementService) ListLocations(ctx context.Context, page PageRequest) (Page[domain.Location], error) {
	if err := s.listReady(); err != nil {
		return Page[domain.Location]{}, err
	}
	limit := NormalizePageLimit(page.Limit)
	request, err := s.stringRequest(page.Cursor, locationsCursorEndpoint, nil, limit)
	if err != nil {
		return Page[domain.Location]{}, err
	}
	var rows []domain.Location
	err = s.view(ctx, func(ctx context.Context, repository port.ManagementQueryRepository) error {
		var err error
		rows, err = repository.ListLocations(ctx, request)
		return err
	})
	if err != nil {
		return Page[domain.Location]{}, wrapManagementRead("list locations", err)
	}
	return s.locationPage(rows, limit)
}

func (s *ManagementService) GetMonitor(ctx context.Context, id domain.MonitorID) (ConfiguredMonitor, error) {
	var record port.MonitorRecord
	err := s.view(ctx, func(ctx context.Context, repository port.ManagementQueryRepository) error {
		var err error
		record, err = repository.GetMonitor(ctx, id)
		return err
	})
	if err != nil {
		return ConfiguredMonitor{}, wrapManagementRead("get monitor", err)
	}
	return configuredMonitor(record), nil
}

func (s *ManagementService) ListMonitors(ctx context.Context, page PageRequest) (Page[ConfiguredMonitor], error) {
	if err := s.listReady(); err != nil {
		return Page[ConfiguredMonitor]{}, err
	}
	limit := NormalizePageLimit(page.Limit)
	request, err := s.intRequest(page.Cursor, monitorsCursorEndpoint, limit)
	if err != nil {
		return Page[ConfiguredMonitor]{}, err
	}
	var rows []port.MonitorRecord
	err = s.view(ctx, func(ctx context.Context, repository port.ManagementQueryRepository) error {
		var err error
		rows, err = repository.ListMonitors(ctx, request)
		return err
	})
	if err != nil {
		return Page[ConfiguredMonitor]{}, wrapManagementRead("list monitors", err)
	}
	return s.monitorPage(rows, limit)
}

func (s *ManagementService) GetAgent(ctx context.Context, id domain.AgentID) (domain.Agent, error) {
	var result domain.Agent
	err := s.view(ctx, func(ctx context.Context, repository port.ManagementQueryRepository) error {
		var err error
		result, err = repository.GetAgent(ctx, id)
		return err
	})
	if err != nil {
		return domain.Agent{}, wrapManagementRead("get agent", err)
	}
	return cloneAgent(result), nil
}

func (s *ManagementService) ListAgents(ctx context.Context, page PageRequest) (Page[domain.Agent], error) {
	if err := s.listReady(); err != nil {
		return Page[domain.Agent]{}, err
	}
	limit := NormalizePageLimit(page.Limit)
	request, err := s.stringRequest(page.Cursor, agentsCursorEndpoint, nil, limit)
	if err != nil {
		return Page[domain.Agent]{}, err
	}
	var rows []domain.Agent
	err = s.view(ctx, func(ctx context.Context, repository port.ManagementQueryRepository) error {
		var err error
		rows, err = repository.ListAgents(ctx, request)
		return err
	})
	if err != nil {
		return Page[domain.Agent]{}, wrapManagementRead("list agents", err)
	}
	return s.agentPage(rows, limit)
}

func (s *ManagementService) GetIncident(ctx context.Context, id domain.IncidentID) (domain.Incident, error) {
	var result domain.Incident
	err := s.view(ctx, func(ctx context.Context, repository port.ManagementQueryRepository) error {
		var err error
		result, err = repository.GetIncident(ctx, id)
		return err
	})
	return cloneIncident(result), wrapManagementRead("get incident", err)
}

func (s *ManagementService) ListIncidents(
	ctx context.Context,
	resolution port.IncidentResolutionFilter,
	page PageRequest,
) (Page[domain.Incident], error) {
	if err := s.listReady(); err != nil {
		return Page[domain.Incident]{}, err
	}
	if resolution != port.IncidentResolutionAll && resolution != port.IncidentResolutionOpen &&
		resolution != port.IncidentResolutionResolved {
		return Page[domain.Incident]{}, &ValidationError{Fields: map[string]string{"state": "must be open or resolved"}}
	}
	limit := NormalizePageLimit(page.Limit)
	audience := CursorAudience{Endpoint: incidentsCursorEndpoint}
	if resolution != port.IncidentResolutionAll {
		audience.Filter = map[string][]string{"state": {string(resolution)}}
	}
	request, err := s.timeRequest(page.Cursor, audience, limit)
	if err != nil {
		return Page[domain.Incident]{}, err
	}
	var rows []domain.Incident
	err = s.view(ctx, func(ctx context.Context, repository port.ManagementQueryRepository) error {
		var err error
		rows, err = repository.ListIncidents(ctx, port.IncidentListRequest{
			TimeKeysetRequest: request,
			Resolution:        resolution,
		})
		return err
	})
	if err != nil {
		return Page[domain.Incident]{}, wrapManagementRead("list incidents", err)
	}
	return s.incidentPage(rows, resolution, limit)
}

func (s *ManagementService) ListIncidentEvents(
	ctx context.Context,
	incidentID domain.IncidentID,
	page PageRequest,
) (Page[domain.IncidentEvent], error) {
	if err := s.listReady(); err != nil {
		return Page[domain.IncidentEvent]{}, err
	}
	limit := NormalizePageLimit(page.Limit)
	audience := CursorAudience{Endpoint: incidentEventsCursorEndpoint, Filter: map[string][]string{
		"incidentId": {string(incidentID)},
	}}
	request, err := s.timeRequest(page.Cursor, audience, limit)
	if err != nil {
		return Page[domain.IncidentEvent]{}, err
	}
	var rows []domain.IncidentEvent
	err = s.view(ctx, func(ctx context.Context, repository port.ManagementQueryRepository) error {
		if _, err := repository.GetIncident(ctx, incidentID); err != nil {
			return err
		}
		var err error
		rows, err = repository.ListIncidentEvents(ctx, incidentID, request)
		return err
	})
	if err != nil {
		return Page[domain.IncidentEvent]{}, wrapManagementRead("list incident events", err)
	}
	return s.incidentEventPage(rows, incidentID, limit)
}

func (s *ManagementService) view(
	ctx context.Context,
	read func(context.Context, port.ManagementQueryRepository) error,
) error {
	if s == nil || s.store == nil {
		return errors.New("management service is not configured")
	}
	return s.store.View(ctx, func(ctx context.Context, repositories Repositories) error {
		if repositories.Management == nil {
			return errors.New("management repository is not configured")
		}
		return read(ctx, repositories.Management)
	})
}

func (s *ManagementService) listReady() error {
	if s == nil || s.store == nil || s.cursors == nil {
		return errors.New("management list service is not configured")
	}
	return nil
}

func (s *ManagementService) stringRequest(
	cursor string,
	endpoint string,
	filter map[string][]string,
	limit int,
) (port.StringKeysetRequest, error) {
	request := port.StringKeysetRequest{Limit: limit + 1}
	if cursor == "" {
		return request, nil
	}
	key, err := s.cursors.DecodeFor(cursor, CursorAudience{Endpoint: endpoint, Filter: filter}, CursorShapeString)
	if err != nil {
		return port.StringKeysetRequest{}, err
	}
	request.AfterSort, request.AfterID, request.HasAfter = key.Sort, key.ID, true
	return request, nil
}

func (s *ManagementService) intRequest(cursor, endpoint string, limit int) (port.IntKeysetRequest, error) {
	request := port.IntKeysetRequest{Limit: limit + 1}
	if cursor == "" {
		return request, nil
	}
	key, err := s.cursors.DecodeFor(cursor, CursorAudience{Endpoint: endpoint}, CursorShapeInt)
	if err != nil {
		return port.IntKeysetRequest{}, err
	}
	sortValue, err := strconv.ParseInt(key.Sort, 10, 64)
	if err != nil {
		return port.IntKeysetRequest{}, invalidCursorError()
	}
	request.AfterSort, request.AfterID, request.HasAfter = sortValue, key.ID, true
	return request, nil
}

func (s *ManagementService) timeRequest(cursor string, audience CursorAudience, limit int) (port.TimeKeysetRequest, error) {
	request := port.TimeKeysetRequest{Limit: limit + 1}
	if cursor == "" {
		return request, nil
	}
	key, err := s.cursors.DecodeFor(cursor, audience, CursorShapeTime)
	if err != nil {
		return port.TimeKeysetRequest{}, err
	}
	sortValue, err := time.Parse(time.RFC3339Nano, key.Sort)
	if err != nil {
		return port.TimeKeysetRequest{}, invalidCursorError()
	}
	request.AfterSort, request.AfterID, request.HasAfter = sortValue.UTC(), key.ID, true
	return request, nil
}

func (s *ManagementService) locationPage(rows []domain.Location, limit int) (Page[domain.Location], error) {
	items, hasMore := trimPage(rows, limit)
	page := Page[domain.Location]{Items: slices.Clone(items)}
	if !hasMore {
		return page, nil
	}
	last := items[len(items)-1]
	cursor, err := s.cursors.EncodeFor(CursorAudience{Endpoint: locationsCursorEndpoint}, CursorKey{Sort: last.Name, ID: string(last.ID)}, CursorShapeString)
	page.NextCursor = cursor
	return page, err
}

func (s *ManagementService) monitorPage(rows []port.MonitorRecord, limit int) (Page[ConfiguredMonitor], error) {
	rows, hasMore := trimPage(rows, limit)
	page := Page[ConfiguredMonitor]{Items: make([]ConfiguredMonitor, len(rows))}
	for index, row := range rows {
		page.Items[index] = configuredMonitor(row)
	}
	if !hasMore {
		return page, nil
	}
	last := rows[len(rows)-1].Monitor
	cursor, err := s.cursors.EncodeFor(CursorAudience{Endpoint: monitorsCursorEndpoint}, CursorKey{Sort: strconv.FormatInt(int64(last.DisplayOrder), 10), ID: string(last.ID)}, CursorShapeInt)
	page.NextCursor = cursor
	return page, err
}

func (s *ManagementService) agentPage(rows []domain.Agent, limit int) (Page[domain.Agent], error) {
	rows, hasMore := trimPage(rows, limit)
	page := Page[domain.Agent]{Items: make([]domain.Agent, len(rows))}
	for index, row := range rows {
		page.Items[index] = cloneAgent(row)
	}
	if !hasMore {
		return page, nil
	}
	last := rows[len(rows)-1]
	cursor, err := s.cursors.EncodeFor(CursorAudience{Endpoint: agentsCursorEndpoint}, CursorKey{Sort: last.Name, ID: string(last.ID)}, CursorShapeString)
	page.NextCursor = cursor
	return page, err
}

func (s *ManagementService) incidentPage(rows []domain.Incident, resolution port.IncidentResolutionFilter, limit int) (Page[domain.Incident], error) {
	rows, hasMore := trimPage(rows, limit)
	page := Page[domain.Incident]{Items: make([]domain.Incident, len(rows))}
	for index, row := range rows {
		page.Items[index] = cloneIncident(row)
	}
	if !hasMore {
		return page, nil
	}
	audience := CursorAudience{Endpoint: incidentsCursorEndpoint}
	if resolution != port.IncidentResolutionAll {
		audience.Filter = map[string][]string{"state": {string(resolution)}}
	}
	last := rows[len(rows)-1]
	cursor, err := s.cursors.EncodeFor(audience, CursorKey{Sort: last.OpenedAt.UTC().Format(time.RFC3339Nano), ID: string(last.ID)}, CursorShapeTime)
	page.NextCursor = cursor
	return page, err
}

func (s *ManagementService) incidentEventPage(rows []domain.IncidentEvent, incidentID domain.IncidentID, limit int) (Page[domain.IncidentEvent], error) {
	rows, hasMore := trimPage(rows, limit)
	page := Page[domain.IncidentEvent]{Items: slices.Clone(rows)}
	if !hasMore {
		return page, nil
	}
	last := rows[len(rows)-1]
	cursor, err := s.cursors.EncodeFor(CursorAudience{Endpoint: incidentEventsCursorEndpoint, Filter: map[string][]string{
		"incidentId": {string(incidentID)},
	}}, CursorKey{Sort: last.CreatedAt.UTC().Format(time.RFC3339Nano), ID: last.ID}, CursorShapeTime)
	page.NextCursor = cursor
	return page, err
}

func trimPage[T any](rows []T, limit int) ([]T, bool) {
	if len(rows) <= limit {
		return rows, false
	}
	return rows[:limit], true
}

func configuredMonitor(record port.MonitorRecord) ConfiguredMonitor {
	monitor := record.Monitor
	monitor.Labels = monitor.MetadataLabels()
	probe := monitor.Probe()
	monitor.HTTP, monitor.TCP, monitor.DNS = probe.HTTP, probe.TCP, probe.DNS
	return ConfiguredMonitor{Monitor: monitor, LocationID: record.LocationID, RequiredLocation: record.RequiredLocation}
}

func cloneAgent(agent domain.Agent) domain.Agent {
	agent.Capabilities = slices.Clone(agent.Capabilities)
	agent.RevokedAt = cloneTime(agent.RevokedAt)
	return agent
}

func cloneIncident(incident domain.Incident) domain.Incident {
	incident.RecoveredAt = cloneTime(incident.RecoveredAt)
	return incident
}

func wrapManagementRead(operation string, err error) error {
	if err == nil || errors.Is(err, ErrNotFound) {
		return err
	}
	return fmt.Errorf("%s: %w", operation, err)
}
