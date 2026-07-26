package contracttest

import (
	"context"
	"errors"
	"testing"
	"time"

	application "github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

// RunManagement executes the public management persistence contract against
// every relational UnitOfWork implementation.
func RunManagement(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("management lifecycle and keysets", func(t *testing.T) {
		testManagementLifecycleAndKeysets(t, factory(t))
	})
	t.Run("management transaction rollback", func(t *testing.T) {
		testManagementTransactionRollback(t, factory(t))
	})
}

func testManagementLifecycleAndKeysets(t *testing.T, store application.UnitOfWork) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 4, 30, 0, 123, time.UTC)
	locationA := managementLocation("00000000-0000-4000-8000-000000000801", "alpha", now)
	locationB := managementLocation("00000000-0000-4000-8000-000000000802", "bravo", now.Add(time.Second))
	monitorA := managementMonitor(t, "00000000-0000-4000-8000-000000000811", "alpha", 7, now)
	monitorB := managementMonitor(t, "00000000-0000-4000-8000-000000000812", "bravo", 7, now.Add(time.Second))
	agentID := domain.AgentID("00000000-0000-4000-8000-000000000821")
	agent, err := domain.NewAgent(domain.NewAgentParams{
		ID: agentID, LocationID: locationA.ID, Name: "agent",
		Capabilities:         []domain.AgentCapability{domain.CapabilityHTTP},
		CredentialGeneration: 1, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	agentSecond, err := domain.NewAgent(domain.NewAgentParams{
		ID: "00000000-0000-4000-8000-000000000822", LocationID: locationB.ID, Name: "agent",
		Capabilities:         []domain.AgentCapability{domain.CapabilityHTTP},
		CredentialGeneration: 1, CreatedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	incidentA := domain.Incident{ID: "00000000-0000-4000-8000-000000000831", MonitorID: monitorA.ID, State: domain.HealthDown, Severity: domain.IncidentCritical, OpenedAt: now.Add(10*time.Minute + 200*time.Microsecond), LastTransitionAt: now.Add(10*time.Minute + 200*time.Microsecond)}
	incidentB := domain.Incident{ID: "00000000-0000-4000-8000-000000000832", MonitorID: monitorB.ID, State: domain.HealthDown, Severity: domain.IncidentCritical, OpenedAt: now.Add(10*time.Minute + 100*time.Microsecond), LastTransitionAt: now.Add(10*time.Minute + 100*time.Microsecond)}

	transact(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		for _, location := range []domain.Location{locationB, locationA} {
			if err := repositories.Locations.Create(ctx, location); err != nil {
				return err
			}
		}
		for _, fixture := range []struct {
			monitor  domain.Monitor
			location domain.LocationID
		}{{monitorB, locationB.ID}, {monitorA, locationA.ID}} {
			if err := repositories.Monitors.Create(ctx, fixture.monitor); err != nil {
				return err
			}
			if err := repositories.Monitors.AssignLocation(ctx, application.MonitorLocation{MonitorID: fixture.monitor.ID, LocationID: fixture.location, Required: true}); err != nil {
				return err
			}
		}
		if err := repositories.Monitors.AssignLocation(ctx, application.MonitorLocation{MonitorID: monitorA.ID, LocationID: locationB.ID, Required: false}); err != nil {
			return err
		}
		if err := repositories.Agents.Create(ctx, application.AgentRecord{Agent: agent, CredentialHash: []byte("generation-1")}); err != nil {
			return err
		}
		if err := repositories.Agents.Create(ctx, application.AgentRecord{Agent: agentSecond, CredentialHash: []byte("second-generation-1")}); err != nil {
			return err
		}
		for _, incident := range []domain.Incident{incidentA, incidentB} {
			if err := repositories.Incidents.Open(ctx, incident); err != nil {
				return err
			}
		}
		for _, event := range []struct {
			id string
			at time.Time
		}{
			{"00000000-0000-4000-8000-000000000841", now.Add(20*time.Minute + 200*time.Microsecond)},
			{"00000000-0000-4000-8000-000000000842", now.Add(20*time.Minute + 100*time.Microsecond)},
		} {
			if err := repositories.Incidents.AppendEvent(ctx, domain.IncidentEvent{ID: event.id, IncidentID: incidentA.ID, Action: domain.NotificationChange, PreviousState: domain.HealthDegraded, State: domain.HealthDown, Severity: domain.IncidentCritical, CreatedAt: event.at}); err != nil {
				return err
			}
		}
		return nil
	})

	view(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		if repositories.Management == nil || repositories.ManagementCommands == nil {
			t.Fatal("management repositories are not wired")
		}
		locations, err := repositories.Management.ListLocations(ctx, application.StringKeysetRequest{Limit: 1})
		if err != nil || len(locations) != 1 || locations[0].ID != locationA.ID {
			t.Fatalf("first location page = %#v, %v", locations, err)
		}
		locations, err = repositories.Management.ListLocations(ctx, application.StringKeysetRequest{Limit: 2, HasAfter: true, AfterSort: locationA.Name, AfterID: string(locationA.ID)})
		if err != nil || len(locations) != 1 || locations[0].ID != locationB.ID {
			t.Fatalf("second location page = %#v, %v", locations, err)
		}
		monitors, err := repositories.Management.ListMonitors(ctx, application.IntKeysetRequest{Limit: 1})
		if err != nil || len(monitors) != 1 || monitors[0].Monitor.ID != monitorA.ID || monitors[0].LocationID != locationA.ID || !monitors[0].RequiredLocation {
			t.Fatalf("first monitor page = %#v, %v", monitors, err)
		}
		monitor, err := repositories.Management.GetMonitor(ctx, monitorA.ID)
		if err != nil || monitor.LocationID != monitors[0].LocationID || monitor.RequiredLocation != monitors[0].RequiredLocation {
			t.Fatalf("monitor get/list assignment mismatch: get=%#v list=%#v error=%v", monitor, monitors[0], err)
		}
		allMonitors, err := repositories.Management.ListMonitors(ctx, application.IntKeysetRequest{Limit: 10})
		if err != nil || len(allMonitors) != 2 || allMonitors[0].Monitor.ID != monitorA.ID || allMonitors[1].Monitor.ID != monitorB.ID {
			t.Fatalf("monitor list with multiple assignments = %#v, %v", allMonitors, err)
		}
		monitors, err = repositories.Management.ListMonitors(ctx, application.IntKeysetRequest{Limit: 2, HasAfter: true, AfterSort: 7, AfterID: string(monitorA.ID)})
		if err != nil || len(monitors) != 1 || monitors[0].Monitor.ID != monitorB.ID {
			t.Fatalf("second monitor page = %#v, %v", monitors, err)
		}
		agents, err := repositories.Management.ListAgents(ctx, application.StringKeysetRequest{Limit: 1})
		if err != nil || len(agents) != 1 || agents[0].ID != agentID {
			t.Fatalf("first agent page = %#v, %v", agents, err)
		}
		agents, err = repositories.Management.ListAgents(ctx, application.StringKeysetRequest{Limit: 2, HasAfter: true, AfterSort: "agent", AfterID: string(agentID)})
		if err != nil || len(agents) != 1 || agents[0].ID != agentSecond.ID {
			t.Fatalf("second agent page = %#v, %v", agents, err)
		}
		incidents, err := repositories.Management.ListIncidents(ctx, application.IncidentListRequest{TimeKeysetRequest: application.TimeKeysetRequest{Limit: 1}})
		if err != nil || len(incidents) != 1 || incidents[0].ID != incidentA.ID {
			t.Fatalf("first incident page = %#v, %v", incidents, err)
		}
		incidents, err = repositories.Management.ListIncidents(ctx, application.IncidentListRequest{TimeKeysetRequest: application.TimeKeysetRequest{Limit: 2, HasAfter: true, AfterSort: incidentA.OpenedAt, AfterID: string(incidentA.ID)}, Resolution: application.IncidentResolutionOpen})
		if err != nil || len(incidents) != 1 || incidents[0].ID != incidentB.ID {
			t.Fatalf("second incident page = %#v, %v", incidents, err)
		}
		events, err := repositories.Management.ListIncidentEvents(ctx, incidentA.ID, application.TimeKeysetRequest{Limit: 1})
		if err != nil || len(events) != 1 || events[0].ID != "00000000-0000-4000-8000-000000000842" {
			t.Fatalf("first event page = %#v, %v", events, err)
		}
		events, err = repositories.Management.ListIncidentEvents(ctx, incidentA.ID, application.TimeKeysetRequest{Limit: 2, HasAfter: true, AfterSort: events[0].CreatedAt, AfterID: events[0].ID})
		if err != nil || len(events) != 1 || events[0].ID != "00000000-0000-4000-8000-000000000841" {
			t.Fatalf("second event page = %#v, %v", events, err)
		}
		return nil
	})

	transact(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		locationA.Name, locationA.UpdatedAt = "alpha-updated", now.Add(time.Hour)
		if changed, err := repositories.ManagementCommands.ReplaceLocation(ctx, locationA); err != nil || !changed {
			t.Fatalf("ReplaceLocation() = %v, %v", changed, err)
		}
		if changed, err := repositories.ManagementCommands.DisableLocation(ctx, locationB.ID, now.Add(time.Hour)); err != nil || !changed {
			t.Fatalf("DisableLocation() = %v, %v", changed, err)
		}
		if changed, err := repositories.ManagementCommands.DisableLocation(ctx, locationB.ID, now.Add(2*time.Hour)); err != nil || changed {
			t.Fatalf("repeated DisableLocation() = %v, %v", changed, err)
		}
		monitorA.Name, monitorA.UpdatedAt = "monitor-updated", now.Add(time.Hour)
		if changed, err := repositories.ManagementCommands.ReplaceMonitor(ctx, application.MonitorRecord{Monitor: monitorA, LocationID: locationB.ID, RequiredLocation: false}); err != nil || !changed {
			t.Fatalf("ReplaceMonitor() = %v, %v", changed, err)
		}
		if changed, err := repositories.ManagementCommands.DisableMonitor(ctx, monitorB.ID, now.Add(time.Hour)); err != nil || !changed {
			t.Fatalf("DisableMonitor() = %v, %v", changed, err)
		}
		agent.Name, agent.LocationID, agent.UpdatedAt = "agent-updated", locationB.ID, now.Add(time.Hour)
		if changed, err := repositories.ManagementCommands.UpdateAgent(ctx, agent); err != nil || !changed {
			t.Fatalf("UpdateAgent() = %v, %v", changed, err)
		}
		created, err := repositories.ManagementCommands.CreateAgentCredentialGeneration(ctx, application.CreateAgentCredentialGenerationCommand{ExpectedCurrentGeneration: 1, Credential: application.AgentCredentialRecord{AgentID: agentID, Generation: 2, CredentialHash: []byte("generation-2"), CreatedAt: now.Add(2 * time.Hour)}})
		if err != nil || !created {
			t.Fatalf("CreateAgentCredentialGeneration() = %v, %v", created, err)
		}
		created, err = repositories.ManagementCommands.CreateAgentCredentialGeneration(ctx, application.CreateAgentCredentialGenerationCommand{ExpectedCurrentGeneration: 2, Credential: application.AgentCredentialRecord{AgentID: agentID, Generation: 3, CredentialHash: []byte("generation-3"), CreatedAt: now.Add(3 * time.Hour)}})
		if err != nil || created {
			t.Fatalf("overlap CreateAgentCredentialGeneration() = %v, %v", created, err)
		}
		outcome, err := repositories.ManagementCommands.RevokeAgentCredentialGeneration(ctx, agentID, 1, now.Add(3*time.Hour))
		if err != nil || outcome != application.CredentialGenerationReplacementUnobserved {
			t.Fatalf("premature revoke = %q, %v", outcome, err)
		}
		if ok, err := repositories.Agents.UpdateHeartbeat(ctx, agentID, 2, "v2", []domain.AgentCapability{domain.CapabilityHTTP}, now.Add(4*time.Hour)); err != nil || !ok {
			t.Fatalf("replacement heartbeat = %v, %v", ok, err)
		}
		outcome, err = repositories.ManagementCommands.RevokeAgentCredentialGeneration(ctx, agentID, 1, now.Add(5*time.Hour))
		if err != nil || outcome != application.CredentialGenerationRevoked {
			t.Fatalf("safe revoke = %q, %v", outcome, err)
		}
		outcome, err = repositories.ManagementCommands.RevokeAgentCredentialGeneration(ctx, agentID, 1, now.Add(6*time.Hour))
		if err != nil || outcome != application.CredentialGenerationAlreadyRevoked {
			t.Fatalf("repeated revoke = %q, %v", outcome, err)
		}
		outcome, err = repositories.ManagementCommands.RevokeAgentCredentialGeneration(ctx, agentID, 2, now.Add(6*time.Hour))
		if err != nil || outcome != application.CredentialGenerationCurrent {
			t.Fatalf("current revoke = %q, %v", outcome, err)
		}
		if revoked, err := repositories.ManagementCommands.RevokeAgent(ctx, agentID, now.Add(7*time.Hour)); err != nil || !revoked {
			t.Fatalf("RevokeAgent() = %v, %v", revoked, err)
		}
		return nil
	})

	view(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		location, err := repositories.Management.GetLocation(ctx, locationB.ID)
		if err != nil || location.Enabled {
			t.Fatalf("disabled location = %#v, %v", location, err)
		}
		monitor, err := repositories.Management.GetMonitor(ctx, monitorA.ID)
		if err != nil || monitor.LocationID != locationB.ID || monitor.RequiredLocation || monitor.Monitor.Name != "monitor-updated" {
			t.Fatalf("replaced monitor = %#v, %v", monitor, err)
		}
		gotAgent, err := repositories.Management.GetAgent(ctx, agentID)
		if err != nil || gotAgent.RevokedAt == nil || gotAgent.Name != "agent-updated" {
			t.Fatalf("revoked agent = %#v, %v", gotAgent, err)
		}
		credential, err := repositories.ManagementCommands.GetAgentCredentialGeneration(ctx, agentID, 2)
		if err != nil || credential.RevokedAt == nil {
			t.Fatalf("current credential after agent revoke = %#v, %v", credential, err)
		}
		due, err := repositories.Monitors.ListDue(ctx, now.Add(24*time.Hour), 10)
		if err != nil || len(due) != 0 {
			t.Fatalf("disabled location/monitor remained schedulable = %#v, %v", due, err)
		}
		return nil
	})
}

func testManagementTransactionRollback(t *testing.T, store application.UnitOfWork) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 7, 26, 5, 0, 0, 0, time.UTC)
	location := managementLocation("00000000-0000-4000-8000-000000000851", "rollback", now)
	monitor := managementMonitor(t, "00000000-0000-4000-8000-000000000852", "rollback", 1, now)
	transact(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		if err := repositories.Locations.Create(ctx, location); err != nil {
			return err
		}
		if err := repositories.Monitors.Create(ctx, monitor); err != nil {
			return err
		}
		return repositories.Monitors.AssignLocation(ctx, application.MonitorLocation{MonitorID: monitor.ID, LocationID: location.ID, Required: true})
	})
	updated := monitor
	updated.Name, updated.UpdatedAt = "must-rollback", now.Add(time.Hour)
	err := store.Transact(ctx, func(ctx context.Context, repositories application.Repositories) error {
		changed, err := repositories.ManagementCommands.ReplaceMonitor(ctx, application.MonitorRecord{Monitor: updated, LocationID: "00000000-0000-4000-8000-000000000899", RequiredLocation: true})
		if err != nil {
			return err
		}
		if !changed {
			t.Fatal("ReplaceMonitor did not reach assignment")
		}
		return nil
	})
	if !errors.Is(err, application.ErrConflict) {
		t.Fatalf("ReplaceMonitor rollback error = %v, want ErrConflict", err)
	}
	view(t, ctx, store, func(ctx context.Context, repositories application.Repositories) error {
		got, err := repositories.Management.GetMonitor(ctx, monitor.ID)
		if err != nil || got.Monitor.Name != monitor.Name || got.LocationID != location.ID {
			t.Fatalf("monitor after rollback = %#v, %v", got, err)
		}
		return nil
	})
}

func managementLocation(id domain.LocationID, name string, at time.Time) domain.Location {
	location, err := domain.NewLocation(id, name, at)
	if err != nil {
		panic(err)
	}
	return location
}

func managementMonitor(t *testing.T, id domain.MonitorID, name string, order int32, at time.Time) domain.Monitor {
	t.Helper()
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{ID: id, Name: name, DisplayOrder: order, Interval: time.Minute, Timeout: 5 * time.Second, FailureThreshold: 2, RecoveryThreshold: 2, HTTP: domain.HTTPProbe{Method: "GET", URL: "https://example.com", ExpectedStatus: []domain.StatusRange{{Min: 200, Max: 299}}}, CreatedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	return monitor
}
