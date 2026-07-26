package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

const (
	managementReadID1 = "00000000-0000-4000-8000-000000000001"
	managementReadID2 = "00000000-0000-4000-8000-000000000002"
)

func TestManagementReadHandlersMapPagesAndRejectCrossEndpointCursor(t *testing.T) {
	now := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	repository := &httpManagementRepository{locations: []domain.Location{
		{ID: managementReadID1, Name: "edge", Enabled: true, CreatedAt: now, UpdatedAt: now},
		{ID: managementReadID2, Name: "public", Enabled: true, CreatedAt: now, UpdatedAt: now},
	}}
	server := managementReadServer(t, repository)
	limit := Limit(1)

	response, err := server.ListLocations(context.Background(), ListLocationsRequestObject{Params: ListLocationsParams{Limit: &limit}})
	if err != nil {
		t.Fatal(err)
	}
	page, ok := response.(ListLocations200JSONResponse)
	if !ok || len(page.Items) != 1 || page.Page.NextCursor == nil || page.Items[0].Enabled == nil || !*page.Items[0].Enabled {
		t.Fatalf("location response = %#v", response)
	}

	cursor := Cursor(*page.Page.NextCursor)
	agents, err := server.ListAgents(context.Background(), ListAgentsRequestObject{Params: ListAgentsParams{Cursor: &cursor}})
	if err != nil {
		t.Fatal(err)
	}
	problem, ok := agents.(ListAgentsdefaultApplicationProblemPlusJSONResponse)
	if !ok || problem.StatusCode != 400 || problem.Body.Code != "validation_failed" {
		t.Fatalf("cross-endpoint response = %#v", agents)
	}
}

func TestManagementReadHandlersMapAgentAndIncidentState(t *testing.T) {
	now := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	recovered := now.Add(time.Minute)
	repository := &httpManagementRepository{
		agent: domain.Agent{
			ID: managementReadID1, LocationID: managementReadID2, Name: "agent",
			CredentialGeneration: 2, Capabilities: []domain.AgentCapability{domain.CapabilityHTTP},
			CreatedAt: now, UpdatedAt: recovered,
		},
		incident: domain.Incident{
			ID: managementReadID1, MonitorID: managementReadID2, Severity: domain.IncidentCritical,
			OpenedAt: now, LastTransitionAt: recovered, RecoveredAt: &recovered,
		},
	}
	server := managementReadServer(t, repository)

	agentResponse, err := server.GetAgent(context.Background(), GetAgentRequestObject{AgentId: mustUUID(t, managementReadID1)})
	if err != nil {
		t.Fatal(err)
	}
	agent := Agent(agentResponse.(GetAgent200JSONResponse))
	if !agent.Enabled || agent.CredentialGeneration != 2 || !agent.UpdatedAt.Equal(recovered) {
		t.Fatalf("agent = %#v", agent)
	}

	incidentResponse, err := server.GetIncident(context.Background(), GetIncidentRequestObject{IncidentId: mustUUID(t, managementReadID1)})
	if err != nil {
		t.Fatal(err)
	}
	incident := Incident(incidentResponse.(GetIncident200JSONResponse))
	if incident.State != IncidentStateResolved || incident.ResolvedAt == nil || !incident.ResolvedAt.Equal(recovered) {
		t.Fatalf("incident = %#v", incident)
	}
}

func managementReadServer(t *testing.T, repository port.ManagementQueryRepository) *Server {
	t.Helper()
	codec, err := application.NewHMACCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewManagementService(application.ManagementServiceConfig{
		Store: &httpManagementStore{repository: repository}, Cursors: codec,
	})
	return NewServer(ServerConfig{Management: service})
}

func mustUUID(t *testing.T, value string) uuid.UUID {
	t.Helper()
	parsed, err := uuid.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

type httpManagementStore struct {
	repository port.ManagementQueryRepository
}

func (s *httpManagementStore) View(ctx context.Context, fn func(context.Context, port.Repositories) error) error {
	return fn(ctx, port.Repositories{Management: s.repository})
}

func (s *httpManagementStore) Transact(ctx context.Context, fn func(context.Context, port.Repositories) error) error {
	return fn(ctx, port.Repositories{Management: s.repository})
}

type httpManagementRepository struct {
	locations []domain.Location
	agent     domain.Agent
	incident  domain.Incident
}

func (r *httpManagementRepository) GetLocation(context.Context, domain.LocationID) (domain.Location, error) {
	if len(r.locations) == 0 {
		return domain.Location{}, port.ErrNotFound
	}
	return r.locations[0], nil
}
func (r *httpManagementRepository) ListLocations(context.Context, port.StringKeysetRequest) ([]domain.Location, error) {
	return r.locations, nil
}
func (*httpManagementRepository) GetMonitor(context.Context, domain.MonitorID) (port.MonitorRecord, error) {
	return port.MonitorRecord{}, port.ErrNotFound
}
func (*httpManagementRepository) ListMonitors(context.Context, port.IntKeysetRequest) ([]port.MonitorRecord, error) {
	return nil, nil
}
func (r *httpManagementRepository) GetAgent(context.Context, domain.AgentID) (domain.Agent, error) {
	if r.agent.ID == "" {
		return domain.Agent{}, port.ErrNotFound
	}
	return r.agent, nil
}
func (*httpManagementRepository) ListAgents(context.Context, port.StringKeysetRequest) ([]domain.Agent, error) {
	return nil, nil
}
func (r *httpManagementRepository) GetIncident(context.Context, domain.IncidentID) (domain.Incident, error) {
	if r.incident.ID == "" {
		return domain.Incident{}, port.ErrNotFound
	}
	return r.incident, nil
}
func (*httpManagementRepository) ListIncidents(context.Context, port.IncidentListRequest) ([]domain.Incident, error) {
	return nil, nil
}
func (*httpManagementRepository) ListIncidentEvents(context.Context, domain.IncidentID, port.TimeKeysetRequest) ([]domain.IncidentEvent, error) {
	return nil, nil
}
