package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

const (
	managementID1 = "00000000-0000-4000-8000-000000000001"
	managementID2 = "00000000-0000-4000-8000-000000000002"
	managementID3 = "00000000-0000-4000-8000-000000000003"
)

func TestListLocationsUsesNameAndStableIDCursor(t *testing.T) {
	repository := &managementQueryRepository{locations: []domain.Location{
		{ID: managementID1, Name: "edge", CreatedAt: time.Unix(1, 0).UTC()},
		{ID: managementID2, Name: "edge", CreatedAt: time.Unix(2, 0).UTC()},
		{ID: managementID3, Name: "public", CreatedAt: time.Unix(3, 0).UTC()},
	}}
	service := newManagementService(t, repository)

	first, err := service.ListLocations(context.Background(), application.PageRequest{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" || repository.locationRequest.Limit != 3 {
		t.Fatalf("first page = %#v, request = %#v", first, repository.locationRequest)
	}

	repository.locations = repository.locations[2:]
	second, err := service.ListLocations(context.Background(), application.PageRequest{Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if !repository.locationRequest.HasAfter || repository.locationRequest.AfterSort != "edge" ||
		repository.locationRequest.AfterID != managementID2 {
		t.Fatalf("decoded location keyset = %#v", repository.locationRequest)
	}
	if len(second.Items) != 1 || second.Items[0].ID != managementID3 || second.NextCursor != "" {
		t.Fatalf("second page = %#v", second)
	}

	_, err = service.ListAgents(context.Background(), application.PageRequest{Cursor: first.NextCursor})
	var validation *application.ValidationError
	if !errors.As(err, &validation) || validation.Fields["cursor"] != "is invalid" {
		t.Fatalf("cross-endpoint cursor error = %#v", err)
	}
}

func TestListMonitorsUsesDisplayOrderAndIDCursor(t *testing.T) {
	repository := &managementQueryRepository{monitors: []port.MonitorRecord{
		{Monitor: domain.Monitor{ID: managementID1, DisplayOrder: 7}},
		{Monitor: domain.Monitor{ID: managementID2, DisplayOrder: 7}},
	}}
	service := newManagementService(t, repository)

	first, err := service.ListMonitors(context.Background(), application.PageRequest{Limit: 1})
	if err != nil || first.NextCursor == "" || len(first.Items) != 1 {
		t.Fatalf("first page = %#v, %v", first, err)
	}
	repository.monitors = repository.monitors[1:]
	if _, err := service.ListMonitors(context.Background(), application.PageRequest{Limit: 1, Cursor: first.NextCursor}); err != nil {
		t.Fatal(err)
	}
	if !repository.monitorRequest.HasAfter || repository.monitorRequest.AfterSort != 7 ||
		repository.monitorRequest.AfterID != managementID1 {
		t.Fatalf("decoded monitor keyset = %#v", repository.monitorRequest)
	}
}

func TestSearchResourcesNormalizesAndBoundsQuery(t *testing.T) {
	repository := &managementQueryRepository{searchResults: []port.SearchResult{{
		ResourceType: port.SearchResourceMonitor, ResourceID: managementID1,
		Title: "Kubernetes API", Description: "LAN control plane", Context: "TCP monitor",
	}}}
	service := newManagementService(t, repository)

	results, err := service.SearchResources(context.Background(), "  kubernetes  ", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || repository.searchRequest.Query != "kubernetes" || repository.searchRequest.Limit != 8 {
		t.Fatalf("results = %#v, request = %#v", results, repository.searchRequest)
	}

	for _, test := range []struct {
		query string
		limit int
	}{
		{query: "x", limit: 8},
		{query: "valid", limit: 21},
	} {
		_, err := service.SearchResources(context.Background(), test.query, test.limit)
		var validation *application.ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("SearchResources(%q, %d) error = %#v", test.query, test.limit, err)
		}
	}
}

func TestListIncidentsBindsResolutionFilterAndOrdersByTimeKey(t *testing.T) {
	opened := time.Date(2026, 7, 26, 8, 9, 10, 11, time.UTC)
	repository := &managementQueryRepository{incidents: []domain.Incident{
		{ID: managementID1, OpenedAt: opened},
		{ID: managementID2, OpenedAt: opened.Add(-time.Minute)},
	}}
	service := newManagementService(t, repository)

	first, err := service.ListIncidents(context.Background(), port.IncidentResolutionOpen, application.PageRequest{Limit: 1})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page = %#v, %v", first, err)
	}
	_, err = service.ListIncidents(context.Background(), port.IncidentResolutionResolved, application.PageRequest{Limit: 1, Cursor: first.NextCursor})
	var validation *application.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("filter-mismatched cursor error = %#v", err)
	}

	repository.incidents = repository.incidents[1:]
	if _, err := service.ListIncidents(context.Background(), port.IncidentResolutionOpen, application.PageRequest{Limit: 1, Cursor: first.NextCursor}); err != nil {
		t.Fatal(err)
	}
	if !repository.incidentRequest.HasAfter || !repository.incidentRequest.AfterSort.Equal(opened) ||
		repository.incidentRequest.AfterID != managementID1 || repository.incidentRequest.Resolution != port.IncidentResolutionOpen {
		t.Fatalf("decoded incident request = %#v", repository.incidentRequest)
	}
}

func TestListIncidentEventsBindsIncidentID(t *testing.T) {
	occurred := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)
	repository := &managementQueryRepository{
		incident: domain.Incident{ID: managementID1},
		events: []domain.IncidentEvent{
			{ID: managementID2, IncidentID: managementID1, CreatedAt: occurred},
			{ID: managementID3, IncidentID: managementID1, CreatedAt: occurred},
		},
	}
	service := newManagementService(t, repository)

	first, err := service.ListIncidentEvents(context.Background(), managementID1, application.PageRequest{Limit: 1})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page = %#v, %v", first, err)
	}
	_, err = service.ListIncidentEvents(context.Background(), managementID2, application.PageRequest{Limit: 1, Cursor: first.NextCursor})
	var validation *application.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("incident-mismatched cursor error = %#v", err)
	}

	repository.events = repository.events[1:]
	if _, err := service.ListIncidentEvents(context.Background(), managementID1, application.PageRequest{Limit: 1, Cursor: first.NextCursor}); err != nil {
		t.Fatal(err)
	}
	if !repository.eventRequest.HasAfter || !repository.eventRequest.AfterSort.Equal(occurred) ||
		repository.eventRequest.AfterID != managementID2 {
		t.Fatalf("decoded event request = %#v", repository.eventRequest)
	}
}

func newManagementService(t *testing.T, repository port.ManagementQueryRepository) *application.ManagementService {
	t.Helper()
	codec, err := application.NewHMACCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return application.NewManagementService(application.ManagementServiceConfig{
		Store: &managementQueryStore{repository: repository}, Cursors: codec,
	})
}

type managementQueryStore struct {
	repository port.ManagementQueryRepository
}

func (s *managementQueryStore) View(ctx context.Context, fn func(context.Context, port.Repositories) error) error {
	return fn(ctx, port.Repositories{Management: s.repository})
}

func (s *managementQueryStore) Transact(ctx context.Context, fn func(context.Context, port.Repositories) error) error {
	return fn(ctx, port.Repositories{Management: s.repository})
}

type managementQueryRepository struct {
	locations       []domain.Location
	monitors        []port.MonitorRecord
	agents          []domain.Agent
	incident        domain.Incident
	incidents       []domain.Incident
	events          []domain.IncidentEvent
	locationRequest port.StringKeysetRequest
	monitorRequest  port.IntKeysetRequest
	agentRequest    port.StringKeysetRequest
	incidentRequest port.IncidentListRequest
	eventRequest    port.TimeKeysetRequest
	searchResults   []port.SearchResult
	searchRequest   port.SearchRequest
}

func (r *managementQueryRepository) SearchResources(_ context.Context, request port.SearchRequest) ([]port.SearchResult, error) {
	r.searchRequest = request
	return r.searchResults, nil
}

func (r *managementQueryRepository) GetLocation(_ context.Context, id domain.LocationID) (domain.Location, error) {
	for _, item := range r.locations {
		if item.ID == id {
			return item, nil
		}
	}
	return domain.Location{}, port.ErrNotFound
}

func (r *managementQueryRepository) ListLocations(_ context.Context, request port.StringKeysetRequest) ([]domain.Location, error) {
	r.locationRequest = request
	return r.locations, nil
}

func (r *managementQueryRepository) GetMonitor(_ context.Context, id domain.MonitorID) (port.MonitorRecord, error) {
	for _, item := range r.monitors {
		if item.Monitor.ID == id {
			return item, nil
		}
	}
	return port.MonitorRecord{}, port.ErrNotFound
}

func (r *managementQueryRepository) ListMonitors(_ context.Context, request port.IntKeysetRequest) ([]port.MonitorRecord, error) {
	r.monitorRequest = request
	return r.monitors, nil
}

func (r *managementQueryRepository) GetAgent(_ context.Context, id domain.AgentID) (domain.Agent, error) {
	for _, item := range r.agents {
		if item.ID == id {
			return item, nil
		}
	}
	return domain.Agent{}, port.ErrNotFound
}

func (r *managementQueryRepository) ListAgents(_ context.Context, request port.StringKeysetRequest) ([]domain.Agent, error) {
	r.agentRequest = request
	return r.agents, nil
}

func (r *managementQueryRepository) GetIncident(_ context.Context, id domain.IncidentID) (domain.Incident, error) {
	if r.incident.ID == id {
		return r.incident, nil
	}
	for _, item := range r.incidents {
		if item.ID == id {
			return item, nil
		}
	}
	return domain.Incident{}, port.ErrNotFound
}

func (r *managementQueryRepository) ListIncidents(_ context.Context, request port.IncidentListRequest) ([]domain.Incident, error) {
	r.incidentRequest = request
	return r.incidents, nil
}

func (r *managementQueryRepository) ListIncidentEvents(_ context.Context, _ domain.IncidentID, request port.TimeKeysetRequest) ([]domain.IncidentEvent, error) {
	r.eventRequest = request
	return r.events, nil
}
