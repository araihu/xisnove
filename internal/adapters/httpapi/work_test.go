package httpapi_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	"github.com/araihu/xisnove/internal/adapters/httpapi"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestLeaseAgentWorkReturnsCompatibleProbeThenNoContent(t *testing.T) {
	server, agentID := newWorkServer(t)
	ctx := httpapi.ContextWithPrincipal(
		context.Background(),
		application.Principal{
			Kind: application.PrincipalAgent, SubjectID: string(agentID),
		},
	)
	request := httpapi.LeaseAgentWorkRequestObject{
		Body: &httpapi.LeaseAgentWorkJSONRequestBody{
			WaitSeconds: 0,
			Capabilities: []httpapi.AgentCapability{
				httpapi.AgentCapabilityHttp,
			},
		},
	}

	response, err := server.LeaseAgentWork(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	work, ok := response.(httpapi.LeaseAgentWork200JSONResponse)
	if !ok {
		t.Fatalf("response = %#v", response)
	}
	probe, err := work.Probe.AsHTTPProbeDefinition()
	if err != nil {
		t.Fatal(err)
	}
	if probe.Url != "https://example.com/health" ||
		work.TimeoutMillis != 5000 ||
		work.LeaseToken == "" {
		t.Fatalf("work = %#v", work)
	}

	response, err = server.LeaseAgentWork(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := response.(httpapi.LeaseAgentWork204Response); !ok {
		t.Fatalf("response = %#v", response)
	}
}

func TestLeaseAgentWorkRejectsCapabilitiesNotAdvertisedByAgent(t *testing.T) {
	server, agentID := newWorkServer(t)
	ctx := httpapi.ContextWithPrincipal(
		context.Background(),
		application.Principal{
			Kind: application.PrincipalAgent, SubjectID: string(agentID),
		},
	)
	response, err := server.LeaseAgentWork(ctx, httpapi.LeaseAgentWorkRequestObject{
		Body: &httpapi.LeaseAgentWorkJSONRequestBody{
			Capabilities: []httpapi.AgentCapability{httpapi.AgentCapabilityTcp},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	problem, ok := response.(httpapi.LeaseAgentWorkdefaultApplicationProblemPlusJSONResponse)
	if !ok || problem.StatusCode != 401 {
		t.Fatalf("response = %#v", response)
	}
}

func newWorkServer(t *testing.T) (*httpapi.Server, domain.AgentID) {
	t.Helper()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(db)
	now := time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC)
	location, err := domain.NewLocation(
		"11111111-1111-4111-8111-111111111111",
		"public",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Repositories().Locations.Create(context.Background(), location); err != nil {
		t.Fatal(err)
	}
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID:                "22222222-2222-4222-8222-222222222222",
		Name:              "website",
		Interval:          time.Minute,
		Timeout:           5 * time.Second,
		FailureThreshold:  3,
		RecoveryThreshold: 2,
		HTTP: domain.HTTPProbe{
			Method:         "GET",
			URL:            "https://example.com/health",
			ExpectedStatus: []domain.StatusRange{{Min: 200, Max: 200}},
		},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	repositories := store.Repositories()
	if err := repositories.Monitors.Create(context.Background(), monitor); err != nil {
		t.Fatal(err)
	}
	if err := repositories.Monitors.AssignLocation(
		context.Background(),
		application.MonitorLocation{
			MonitorID: monitor.ID, LocationID: location.ID, Required: true,
		},
	); err != nil {
		t.Fatal(err)
	}
	scheduler := application.NewScheduler(
		store,
		func() string { return "33333333-3333-4333-8333-333333333333" },
	)
	if inserted, err := scheduler.EnqueueDue(context.Background(), 1); err != nil {
		t.Fatal(err)
	} else if inserted != 1 {
		t.Fatalf("inserted = %d", inserted)
	}
	agent, err := domain.NewAgent(domain.NewAgentParams{
		ID:                   "44444444-4444-4444-8444-444444444444",
		LocationID:           location.ID,
		Name:                 "vps-1",
		Capabilities:         []domain.AgentCapability{domain.CapabilityHTTP},
		CredentialGeneration: 1,
		CreatedAt:            now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Agents.Create(
		context.Background(),
		application.AgentRecord{Agent: agent, CredentialHash: []byte("credential")},
	); err != nil {
		t.Fatal(err)
	}
	lease := application.NewLeaseService(application.LeaseServiceConfig{
		Store: store,
		Tokens: xiscrypto.NewTokenIssuer(
			bytes.NewReader(bytes.Repeat([]byte{0x42}, 64)),
		),
		LeaseDuration: 30 * time.Second,
	})
	return httpapi.NewServer(httpapi.ServerConfig{Lease: lease}), agent.ID
}
