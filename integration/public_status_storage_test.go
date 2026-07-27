package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	"github.com/araihu/xisnove/internal/adapters/database"
	"github.com/araihu/xisnove/internal/adapters/httpapi"
	"github.com/araihu/xisnove/internal/adapters/ids"
	"github.com/araihu/xisnove/sdk"
)

func TestPublicStatusStorageMatrix(t *testing.T) {
	t.Run("SQLite", func(t *testing.T) {
		runPublicStatusJourney(t, newFileStorageHarness(t, database.ProfileSQLite))
	})
	t.Run("TursoLocal", func(t *testing.T) {
		runPublicStatusJourney(t, newFileStorageHarness(t, database.ProfileTursoLocal))
	})
	t.Run("Postgres", func(t *testing.T) {
		runPublicStatusJourney(t, newPostgresStorageHarness(t))
	})
	t.Run("TursoCloud", func(t *testing.T) {
		runPublicStatusJourney(t, newTursoCloudStorageHarness(t))
	})
}

func runPublicStatusJourney(t *testing.T, harness *storageHarness) {
	t.Helper()
	ctx := context.Background()
	store := harness.primary.Store
	tokens := xiscrypto.NewProductionTokenIssuer()
	const (
		adminEmail    = "status-admin@example.com"
		adminPassword = "correct horse battery staple"
	)
	statusNow := time.Now().UTC()
	auth := application.NewAuthService(application.AuthServiceConfig{
		Store: store, Passwords: xiscrypto.NewProductionPasswordHasher(), Tokens: tokens,
		SessionDuration: time.Hour, Now: func() time.Time { return statusNow }, NewID: ids.NewUUID,
	})
	if err := auth.BootstrapAdmin(ctx, adminEmail, adminPassword); err != nil {
		t.Fatal(err)
	}
	agents := application.NewAgentService(application.AgentServiceConfig{
		Store: store, Tokens: tokens, Now: func() time.Time { return statusNow }, NewID: ids.NewUUID,
	})
	publicStatus, err := application.NewPublicStatusService(application.PublicStatusServiceConfig{
		Store: harness.primary.PublicStatusUnitOfWork(), Now: func() time.Time { return statusNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.NewHandler(httpapi.HandlerConfig{
		Server: httpapi.NewServer(httpapi.ServerConfig{
			Auth: auth,
			Configuration: application.NewConfigurationService(
				store, func() time.Time { return statusNow.Add(-time.Second) }, ids.NewUUID,
			),
			Agents:       agents,
			PublicStatus: publicStatus,
			Lease: application.NewLeaseService(application.LeaseServiceConfig{
				Store: store, Tokens: tokens, LeaseDuration: time.Minute,
			}),
			Results: application.NewResultService(application.ResultServiceConfig{
				Store: store, Tokens: tokens, Now: func() time.Time { return statusNow },
				NewID: ids.NewUUID, LeaseDuration: time.Minute,
			}),
			Health: application.NewHealthService(store),
		}),
		Ready: harness.primary.Ready,
	})
	if err != nil {
		t.Fatal(err)
	}

	setupServer := httptest.NewServer(handler)
	setupClient, err := sdk.NewClientWithResponses(setupServer.URL)
	if err != nil {
		setupServer.Close()
		t.Fatal(err)
	}
	fixtures := createPublicStatusFixtures(t, ctx, setupClient, store, tokens, statusNow, adminEmail, adminPassword)
	setupServer.Close()

	// Daily uptime has no mutation endpoint. Prepare its derived projection only
	// after the API fixture server is closed and before the assertion server starts.
	seedPublicStatusHistory(t, ctx, store, fixtures.downID, statusNow)

	assertionServer := httptest.NewServer(handler)
	t.Cleanup(assertionServer.Close)
	client, err := sdk.NewClientWithResponses(assertionServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	assertPublicStatusResponse(t, ctx, client, fixtures, statusNow)
}

type publicStatusFixtures struct {
	downID    openapi_types.UUID
	upID      openapi_types.UUID
	privateID openapi_types.UUID
}

func createPublicStatusFixtures(
	t *testing.T,
	ctx context.Context,
	client *sdk.ClientWithResponses,
	store port.UnitOfWork,
	tokens application.TokenIssuer,
	statusNow time.Time,
	email string,
	password string,
) publicStatusFixtures {
	t.Helper()
	session, err := client.CreateSessionWithResponse(ctx, sdk.CreateSessionRequest{
		Email: openapi_types.Email(email), Password: pointer(password),
	})
	if err != nil || session.JSON201 == nil {
		t.Fatalf("create status fixture session response=%#v err=%v", session, err)
	}
	adminAuth := bearer(session.JSON201.Token)
	location, err := client.CreateLocationWithResponse(
		ctx, nil, sdk.CreateLocationRequest{Name: "status-edge"}, adminAuth,
	)
	if err != nil || location.JSON201 == nil {
		t.Fatalf("create status fixture location response=%#v err=%v", location, err)
	}
	probe := clientReadyHTTPProbe(t)
	createMonitor := func(name, description string, order int32, public bool) openapi_types.UUID {
		t.Helper()
		response, err := client.CreateMonitorWithResponse(ctx, nil, sdk.CreateMonitorRequest{
			Name: name, Description: &description, DisplayOrder: &order, Public: &public,
			LocationId: location.JSON201.Id, RequiredLocation: true,
			IntervalSeconds: 60, TimeoutMillis: 5000,
			FailureThreshold: 1, RecoveryThreshold: 1, Probe: probe,
		}, adminAuth)
		if err != nil || response.JSON201 == nil {
			t.Fatalf("create status fixture monitor %q response=%#v err=%v", name, response, err)
		}
		return response.JSON201.Id
	}
	fixtures := publicStatusFixtures{
		downID:    createMonitor("alpha-down", "public down edge", 10, true),
		upID:      createMonitor("zeta-up", "public healthy edge", 20, true),
		privateID: createMonitor("private-vpn-secret", "tailscale-private-description", 0, false),
	}

	enrollment, err := client.CreateAgentEnrollmentTokenWithResponse(ctx, nil,
		sdk.CreateAgentEnrollmentTokenRequest{LocationId: location.JSON201.Id, ExpiresInSeconds: 300},
		adminAuth,
	)
	if err != nil || enrollment.JSON201 == nil {
		t.Fatalf("create status fixture enrollment response=%#v err=%v", enrollment, err)
	}
	credential := "public-status-agent-credential-0000000000000001"
	agent, err := client.EnrollAgentWithResponse(ctx, &sdk.EnrollAgentParams{
		IdempotencyKey: sdk.RequiredIdempotencyKey("public-status-agent-enrollment"),
	}, sdk.EnrollAgentRequest{
		Token: pointer(enrollment.JSON201.Token), Name: "status-agent",
		Credential:   &credential,
		Capabilities: []sdk.AgentCapability{sdk.AgentCapabilityHttp},
	})
	if err != nil || agent.JSON201 == nil {
		t.Fatalf("enroll status fixture agent response=%#v err=%v", agent, err)
	}
	agentAuth := bearer(agent.JSON201.Credential)

	schedulerCtx, stopScheduler := context.WithCancel(ctx)
	var schedulerWG sync.WaitGroup
	schedulerWG.Add(1)
	go func() {
		defer schedulerWG.Done()
		scheduler := application.NewScheduler(store, ids.NewUUID)
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			_, _ = scheduler.EnqueueDue(schedulerCtx, 10)
			select {
			case <-schedulerCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	t.Cleanup(func() {
		stopScheduler()
		schedulerWG.Wait()
	})
	seen := make(map[openapi_types.UUID]bool, 3)
	deadline := time.Now().Add(5 * time.Second)
	for len(seen) < 3 && time.Now().Before(deadline) {
		lease, err := client.LeaseAgentWorkWithResponse(ctx, sdk.LeaseWorkRequest{
			WaitSeconds: 1, Capabilities: []sdk.AgentCapability{sdk.AgentCapabilityHttp},
		}, agentAuth)
		if err != nil {
			t.Fatal(err)
		}
		if lease.JSON200 == nil {
			continue
		}
		work := lease.JSON200
		failed := work.MonitorId != fixtures.upID
		outcome, errorCode, observedStatus, diagnostic := sdk.Passed, sdk.Empty, int32(200), ""
		if failed {
			outcome, errorCode, observedStatus = sdk.Failed, sdk.StatusMismatch, 503
			diagnostic = "private-agent-diagnostic-secret"
		}
		startedAt := work.ScheduledFor.UTC()
		result, err := client.UploadProbeResultsWithResponse(ctx, sdk.ProbeResultBatch{
			Results: []sdk.ProbeResultInput{{
				ResultId: uuid.New(), RunId: work.RunId, LeaseToken: work.LeaseToken,
				StartedAt: startedAt, FinishedAt: startedAt.Add(time.Millisecond),
				Outcome: outcome, LatencyMillis: 1, ObservedStatus: observedStatus,
				BodyAssertionPassed: !failed, ErrorCode: errorCode, DiagnosticSample: diagnostic,
			}},
		}, agentAuth)
		if err != nil || result.JSON200 == nil || len(result.JSON200.Acknowledgements) != 1 ||
			result.JSON200.Acknowledgements[0].Status != sdk.Accepted {
			t.Fatalf("upload status fixture result response=%#v err=%v", result, err)
		}
		seen[work.MonitorId] = true
	}
	stopScheduler()
	schedulerWG.Wait()
	if len(seen) != 3 {
		t.Fatalf("leased status fixture monitors=%v, want all three", seen)
	}
	return fixtures
}

func seedPublicStatusHistory(
	t *testing.T,
	ctx context.Context,
	store port.UnitOfWork,
	monitorID openapi_types.UUID,
	now time.Time,
) {
	t.Helper()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	err := store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		for daysAgo := 31; daysAgo >= 1; daysAgo-- {
			if err := repositories.Retention.UpsertDailyUptime(ctx, port.DailyUptimeRecord{
				MonitorID: stringMonitorID(monitorID), Day: today.AddDate(0, 0, -daysAgo),
				Passing: 1, Failing: 1, Observed: 2 * time.Millisecond, UpdatedAt: now,
			}); err != nil {
				return fmt.Errorf("seed daily uptime %d days ago: %w", daysAgo, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertPublicStatusResponse(
	t *testing.T,
	ctx context.Context,
	client *sdk.ClientWithResponses,
	fixtures publicStatusFixtures,
	now time.Time,
) {
	t.Helper()
	rawResponse, err := client.GetPublicStatusPage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rawResponse.Body.Close()
	body, err := io.ReadAll(rawResponse.Body)
	if err != nil {
		t.Fatal(err)
	}
	if rawResponse.StatusCode != http.StatusOK {
		t.Fatalf("anonymous public status code=%d body=%s", rawResponse.StatusCode, body)
	}
	var page sdk.PublicStatusPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode anonymous public status: %v body=%s", err, body)
	}
	assertPublicStatusPage(t, page, fixtures, now)
	assertPublicStatusRawPrivacy(t, body)

	invalidBearer, err := client.GetPublicStatusPageWithResponse(ctx, bearer("invalid-public-bearer"))
	if err != nil || invalidBearer.JSON200 == nil {
		t.Fatalf("invalid bearer public status response=%#v err=%v", invalidBearer, err)
	}
	if !reflect.DeepEqual(page, *invalidBearer.JSON200) {
		t.Fatalf("invalid bearer changed anonymous status: anonymous=%#v invalid=%#v", page, invalidBearer.JSON200)
	}
}

func assertPublicStatusPage(
	t *testing.T,
	page sdk.PublicStatusPage,
	fixtures publicStatusFixtures,
	now time.Time,
) {
	t.Helper()
	if !page.GeneratedAt.Equal(now) {
		t.Fatalf("generatedAt=%s want=%s", page.GeneratedAt, now)
	}
	if page.State != sdk.Down {
		t.Fatalf("aggregate state=%q want down", page.State)
	}
	if len(page.Monitors) != 2 {
		t.Fatalf("public monitors=%#v, want exactly two", page.Monitors)
	}
	if page.Monitors[0].Id != fixtures.downID || page.Monitors[0].Name != "alpha-down" ||
		page.Monitors[0].State != sdk.Down || page.Monitors[1].Id != fixtures.upID ||
		page.Monitors[1].Name != "zeta-up" || page.Monitors[1].State != sdk.Up {
		t.Fatalf("public monitor order/state=%#v", page.Monitors)
	}
	if page.Monitors[0].Id == fixtures.privateID || page.Monitors[1].Id == fixtures.privateID {
		t.Fatalf("private monitor leaked into public page: %#v", page.Monitors)
	}
	if len(page.ActiveIncidents) != 1 || page.ActiveIncidents[0].MonitorId != fixtures.downID ||
		page.ActiveIncidents[0].MonitorName != "alpha-down" ||
		page.ActiveIncidents[0].State != sdk.PublicIncidentSummaryStateOpen ||
		page.ActiveIncidents[0].Severity != sdk.Critical {
		t.Fatalf("active incidents=%#v, want down monitor incident", page.ActiveIncidents)
	}
	uptime := page.Monitors[0].RecentUptime
	if len(uptime) != 30 {
		t.Fatalf("default uptime history length=%d want=30: %#v", len(uptime), uptime)
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if !uptime[0].Date.Time.Equal(today.AddDate(0, 0, -30)) ||
		!uptime[len(uptime)-1].Date.Time.Equal(today.AddDate(0, 0, -1)) {
		t.Fatalf("default uptime bounds=%s..%s", uptime[0].Date.Time, uptime[len(uptime)-1].Date.Time)
	}
	for _, point := range uptime {
		if point.UptimePercentage != 50 {
			t.Fatalf("uptime point=%#v want 50 percent", point)
		}
	}
}

func assertPublicStatusRawPrivacy(t *testing.T, body []byte) {
	t.Helper()
	lower := strings.ToLower(string(body))
	for _, forbiddenValue := range []string{
		"private-vpn-secret", "tailscale-private-description", "private-agent-diagnostic-secret",
		"https://example.test/health", "status-edge", "status-agent",
	} {
		if strings.Contains(lower, strings.ToLower(forbiddenValue)) {
			t.Fatalf("public status leaked private value %q: %s", forbiddenValue, body)
		}
	}
	var document any
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	forbiddenKeys := map[string]struct{}{
		"probe": {}, "location": {}, "locations": {}, "locationid": {},
		"agent": {}, "agentid": {}, "agents": {}, "credential": {}, "credentials": {},
		"diagnostic": {}, "diagnosticsample": {}, "observedvalues": {}, "headers": {},
		"body": {}, "labels": {}, "leasetoken": {}, "token": {}, "secret": {},
	}
	assertNoPublicStatusKeys(t, document, forbiddenKeys)
}

func assertNoPublicStatusKeys(t *testing.T, value any, forbidden map[string]struct{}) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if _, found := forbidden[strings.ToLower(key)]; found {
				t.Fatalf("public status leaked forbidden field %q", key)
			}
			assertNoPublicStatusKeys(t, child, forbidden)
		}
	case []any:
		for _, child := range typed {
			assertNoPublicStatusKeys(t, child, forbidden)
		}
	}
}

func stringMonitorID(id openapi_types.UUID) domain.MonitorID {
	return domain.MonitorID(id.String())
}
