package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
	alertmanageradapter "github.com/araihu/xisnove/internal/adapters/alertmanager"
	"github.com/araihu/xisnove/internal/adapters/crypto"
	"github.com/araihu/xisnove/internal/adapters/httpapi"
	"github.com/araihu/xisnove/internal/adapters/ids"
	shoutrrradapter "github.com/araihu/xisnove/internal/adapters/shoutrrr"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
	"github.com/araihu/xisnove/sdk"
)

func TestNotificationJourneyTransportsRetriesResolutionAndRedaction(t *testing.T) {
	ctx := context.Background()

	type receivedRequest struct {
		path          string
		authorization string
		body          []byte
	}
	type receiver struct {
		mu       sync.Mutex
		requests []receivedRequest
	}
	record := func(receiver *receiver, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		receiver.mu.Lock()
		defer receiver.mu.Unlock()
		receiver.requests = append(receiver.requests, receivedRequest{
			path: request.URL.Path, authorization: request.Header.Get("Authorization"), body: body,
		})
	}
	snapshot := func(receiver *receiver) []receivedRequest {
		receiver.mu.Lock()
		defer receiver.mu.Unlock()
		return append([]receivedRequest(nil), receiver.requests...)
	}

	var shoutrrrOK, shoutrrrRetry, alertmanagerOK receiver
	shoutrrrOKServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		record(&shoutrrrOK, request)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(shoutrrrOKServer.Close)
	shoutrrrRetryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		record(&shoutrrrRetry, request)
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(shoutrrrRetryServer.Close)
	alertmanagerOKServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		record(&alertmanagerOK, request)
		w.Header().Set("X-Request-ID", "alertmanager-receipt")
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(alertmanagerOKServer.Close)
	alertmanagerTimeoutServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(alertmanagerTimeoutServer.Close)

	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "notification-journey.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := sqlitestore.NewStore(db)
	tokens := crypto.NewProductionTokenIssuer()
	passwords := crypto.NewProductionPasswordHasher()
	envelope, err := crypto.NewEnvelope(
		1, map[uint32][]byte{1: bytes.Repeat([]byte{7}, 32)},
		bytes.NewReader(bytes.Repeat([]byte{3}, 4096)),
	)
	if err != nil {
		t.Fatal(err)
	}
	auth := application.NewAuthService(application.AuthServiceConfig{
		Store: store, Passwords: passwords, Tokens: tokens,
		SessionDuration: time.Hour, Now: time.Now, NewID: ids.NewUUID,
	})
	const password = "correct horse battery staple"
	if err := auth.BootstrapAdmin(ctx, "admin@example.com", password); err != nil {
		t.Fatal(err)
	}
	agents := application.NewAgentService(application.AgentServiceConfig{
		Store: store, Tokens: tokens, Now: time.Now, NewID: ids.NewUUID,
	})
	scheduler := application.NewScheduler(store, ids.NewUUID)
	notifications := application.NewNotificationAdminService(application.NotificationAdminServiceConfig{
		Store: store, Sealer: envelope, Now: time.Now, NewID: ids.NewUUID,
	})
	apiServer := httpapi.NewServer(httpapi.ServerConfig{
		Auth:          auth,
		Configuration: application.NewConfigurationService(store, time.Now, ids.NewUUID),
		Agents:        agents,
		Lease: application.NewLeaseService(application.LeaseServiceConfig{
			Store: store, Tokens: tokens, LeaseDuration: time.Minute,
		}),
		Results: application.NewResultService(application.ResultServiceConfig{
			Store: store, Tokens: tokens, Now: time.Now, NewID: ids.NewUUID,
		}),
		Health: application.NewHealthService(store), Notifications: notifications,
	})
	handler, err := httpapi.NewHandler(httpapi.HandlerConfig{
		Server: apiServer, Ready: func(ctx context.Context) error { return sqlitestore.Ready(ctx, db) },
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := sdk.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	session, err := client.CreateSessionWithResponse(ctx, sdk.CreateSessionRequest{
		Email: openapi_types.Email("admin@example.com"), Password: pointer(password),
	})
	if err != nil || session.JSON201 == nil {
		t.Fatalf("session: response=%#v error=%v", session, err)
	}
	adminAuth := bearer(session.JSON201.Token)
	location, err := client.CreateLocationWithResponse(ctx, nil, sdk.CreateLocationRequest{Name: "notification edge"}, adminAuth)
	if err != nil || location.JSON201 == nil {
		t.Fatalf("location: response=%#v error=%v", location, err)
	}
	var probe sdk.ProbeDefinition
	if err := probe.FromHTTPProbeDefinition(sdk.HTTPProbeDefinition{
		Method: sdk.GET, Url: "https://router.example.test/healthz", Headers: map[string]string{}, Body: []byte{},
		ExpectedStatus: []sdk.StatusRange{{Minimum: 200, Maximum: 299}}, BodyContains: []string{}, BodyDoesNotContain: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{"environment": "homelab"}
	monitor, err := client.CreateMonitorWithResponse(ctx, nil, sdk.CreateMonitorRequest{
		Name: "router", Description: pointer("hybrid edge router"), Labels: &labels,
		LocationId: location.JSON201.Id, RequiredLocation: true, IntervalSeconds: 60,
		TimeoutMillis: 5000, FailureThreshold: 1, RecoveryThreshold: 1, Probe: probe,
	}, adminAuth)
	if err != nil || monitor.JSON201 == nil {
		t.Fatalf("monitor: response=%#v error=%v", monitor, err)
	}

	const shoutrrrSecret = "journey-shoutrrr-secret"
	const alertmanagerSecret = "journey-alertmanager-secret"
	shoutrrrURL := func(target string) string {
		parsed, parseErr := url.Parse(target)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		return fmt.Sprintf("generic://%s/?disabletls=yes&template=json&token=%s", parsed.Host, shoutrrrSecret)
	}
	type channelSpec struct {
		name          string
		configuration sdk.NotificationChannelConfigurationInput
	}
	shoutrrrConfig := func(target string) sdk.NotificationChannelConfigurationInput {
		var configuration sdk.NotificationChannelConfigurationInput
		if err := configuration.FromShoutrrrChannelConfigurationInput(sdk.ShoutrrrChannelConfigurationInput{
			Kind: sdk.ShoutrrrChannelConfigurationInputKindShoutrrr, ServiceUrl: pointer(shoutrrrURL(target)),
		}); err != nil {
			t.Fatal(err)
		}
		return configuration
	}
	alertmanagerConfig := func(target string) sdk.NotificationChannelConfigurationInput {
		var configuration sdk.NotificationChannelConfigurationInput
		if err := configuration.FromAlertmanagerChannelConfigurationInput(sdk.AlertmanagerChannelConfigurationInput{
			Kind: sdk.AlertmanagerChannelConfigurationInputKindAlertmanager, Endpoint: target,
			BearerToken: pointer(alertmanagerSecret),
		}); err != nil {
			t.Fatal(err)
		}
		return configuration
	}
	channelSpecs := []channelSpec{
		{name: "shoutrrr-ok", configuration: shoutrrrConfig(shoutrrrOKServer.URL)},
		{name: "shoutrrr-retry", configuration: shoutrrrConfig(shoutrrrRetryServer.URL)},
		{name: "alertmanager-ok", configuration: alertmanagerConfig(alertmanagerOKServer.URL)},
		{name: "alertmanager-timeout", configuration: alertmanagerConfig(alertmanagerTimeoutServer.URL)},
	}
	var publicBodies [][]byte
	channelIDs := make(map[string]uuid.UUID, len(channelSpecs))
	for index, spec := range channelSpecs {
		created, createErr := client.CreateNotificationChannelWithResponse(ctx, nil, sdk.CreateNotificationChannelRequest{
			Name: spec.name, Enabled: true, Configuration: spec.configuration,
		}, adminAuth)
		if createErr != nil || created.JSON201 == nil {
			t.Fatalf("create channel %s: response=%#v error=%v", spec.name, created, createErr)
		}
		publicBodies = append(publicBodies, created.Body)
		channelIDs[spec.name] = created.JSON201.Id
		route, routeErr := client.CreateNotificationRouteWithResponse(ctx, nil, sdk.NotificationRouteInput{
			Name: spec.name, ChannelId: created.JSON201.Id, MonitorId: &monitor.JSON201.Id,
			Actions:    []sdk.NotificationAction{sdk.NotificationActionOpen, sdk.NotificationActionRecover},
			Severities: []sdk.IncidentSeverity{}, LabelMatchers: map[string]string{},
			Template: "{{ .MonitorName }} transitioned {{ .PreviousState }} -> {{ .State }}",
			Enabled:  true, Precedence: int32(index),
		}, adminAuth)
		if routeErr != nil || route.JSON201 == nil {
			t.Fatalf("create route %s: response=%#v error=%v", spec.name, route, routeErr)
		}
		publicBodies = append(publicBodies, route.Body)
	}

	enrollment, err := client.CreateAgentEnrollmentTokenWithResponse(ctx, nil, sdk.CreateAgentEnrollmentTokenRequest{
		LocationId: location.JSON201.Id, ExpiresInSeconds: 300,
	}, adminAuth)
	if err != nil || enrollment.JSON201 == nil {
		t.Fatalf("enrollment token: response=%#v error=%v", enrollment, err)
	}
	enrolled, err := client.EnrollAgentWithResponse(ctx, sdk.EnrollAgentRequest{
		Token: pointer(enrollment.JSON201.Token), Name: "notification-agent",
		Capabilities: []sdk.AgentCapability{sdk.AgentCapabilityHttp},
	})
	if err != nil || enrolled.JSON201 == nil {
		t.Fatalf("enroll agent: response=%#v error=%v", enrolled, err)
	}
	agentAuth := bearer(enrolled.JSON201.Credential)

	shoutrrrTransport, err := shoutrrradapter.NewTransport(shoutrrradapter.TransportConfig{
		HTTPClient: http.DefaultClient, Timeout: 200 * time.Millisecond, MaxParallel: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	alertmanagerTransport, err := alertmanageradapter.NewTransport(alertmanageradapter.TransportConfig{
		HTTPClient: http.DefaultClient, Timeout: 30 * time.Millisecond, MaxParallel: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := application.NewDeliveryWorker(application.DeliveryWorkerConfig{
		Store: store, Sealer: envelope, Tokens: tokens, NewID: ids.NewUUID, Owner: "notification-journey",
		Transports: map[domain.NotificationChannelKind]application.NotificationTransport{
			domain.NotificationChannelShoutrrr:     shoutrrrTransport,
			domain.NotificationChannelAlertmanager: alertmanagerTransport,
		},
		BatchSize: 20, Concurrency: 4, LeaseDuration: time.Second, SendTimeout: 100 * time.Millisecond,
		MaxAttempts: 3, BackoffBase: time.Minute, BackoffCap: time.Minute, Jitter: func() float64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}

	uploadObservation := func(outcome sdk.ProbeResultInputOutcome) {
		t.Helper()
		if _, updateErr := db.ExecContext(ctx, "UPDATE monitors SET next_run_at = ? WHERE id = ?", time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano), monitor.JSON201.Id.String()); updateErr != nil {
			t.Fatal(updateErr)
		}
		if count, enqueueErr := scheduler.EnqueueDue(ctx, 1); enqueueErr != nil || count != 1 {
			t.Fatalf("enqueue: count=%d error=%v", count, enqueueErr)
		}
		lease, leaseErr := client.LeaseAgentWorkWithResponse(ctx, sdk.LeaseWorkRequest{
			WaitSeconds: 0, Capabilities: []sdk.AgentCapability{sdk.AgentCapabilityHttp},
		}, agentAuth)
		if leaseErr != nil || lease.JSON200 == nil {
			t.Fatalf("lease: response=%#v error=%v", lease, leaseErr)
		}
		// Keep the projected event safely behind the database claim clock so the
		// newly-created outbox rows are immediately due even on coarse clocks.
		startedAt := time.Now().UTC().Add(-time.Second)
		result := sdk.ProbeResultInput{
			ResultId: uuid.New(), RunId: lease.JSON200.RunId, LeaseToken: lease.JSON200.LeaseToken,
			StartedAt: startedAt, FinishedAt: startedAt.Add(time.Millisecond), Outcome: outcome, LatencyMillis: 1,
			ObservedStatus: 200, BodyAssertionPassed: true,
		}
		if outcome == sdk.Failed {
			result.ObservedStatus = 503
			result.BodyAssertionPassed = false
			result.ErrorCode = sdk.StatusMismatch
			result.DiagnosticSample = "HTTP 503"
		}
		uploaded, uploadErr := client.UploadProbeResultsWithResponse(ctx, sdk.ProbeResultBatch{Results: []sdk.ProbeResultInput{result}}, agentAuth)
		if uploadErr != nil || uploaded.JSON200 == nil || uploaded.JSON200.Acknowledgements[0].Status != sdk.Accepted {
			t.Fatalf("upload: response=%#v error=%v", uploaded, uploadErr)
		}
	}

	uploadObservation(sdk.Failed)
	if count, err := worker.RunOnce(ctx); err != nil || count != 4 {
		t.Fatalf("open delivery cycle: count=%d error=%v", count, err)
	}
	uploadObservation(sdk.Passed)
	if count, err := worker.RunOnce(ctx); err != nil || count != 4 {
		t.Fatalf("recovery delivery cycle: count=%d error=%v", count, err)
	}

	shoutrrrRequests := snapshot(&shoutrrrOK)
	if len(shoutrrrRequests) != 2 {
		t.Fatalf("successful Shoutrrr requests = %d", len(shoutrrrRequests))
	}
	for index, expected := range []string{"router transitioned pending -> down", "router transitioned down -> up"} {
		var payload map[string]string
		if err := json.Unmarshal(shoutrrrRequests[index].body, &payload); err != nil || payload["message"] != expected || payload["title"] == "" {
			t.Fatalf("Shoutrrr payload %d = %s, %v", index, shoutrrrRequests[index].body, err)
		}
	}
	if len(snapshot(&shoutrrrRetry)) != 2 {
		t.Fatalf("retryable Shoutrrr requests = %d", len(snapshot(&shoutrrrRetry)))
	}

	alertRequests := snapshot(&alertmanagerOK)
	if len(alertRequests) != 2 {
		t.Fatalf("successful Alertmanager requests = %d", len(alertRequests))
	}
	type alertPayload struct {
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
		StartsAt    string            `json:"startsAt"`
		EndsAt      string            `json:"endsAt"`
	}
	decodeAlert := func(raw []byte) alertPayload {
		t.Helper()
		var alerts []alertPayload
		if err := json.Unmarshal(raw, &alerts); err != nil || len(alerts) != 1 {
			t.Fatalf("Alertmanager payload = %s, %v", raw, err)
		}
		return alerts[0]
	}
	firing, resolved := decodeAlert(alertRequests[0].body), decodeAlert(alertRequests[1].body)
	if alertRequests[0].path != "/api/v2/alerts" || alertRequests[0].authorization != "Bearer "+alertmanagerSecret {
		t.Fatalf("Alertmanager request metadata = %#v", alertRequests[0])
	}
	if firing.Labels["xisnove_fingerprint"] == "" || firing.Labels["xisnove_fingerprint"] != resolved.Labels["xisnove_fingerprint"] || firing.EndsAt != "" || resolved.EndsAt == "" {
		t.Fatalf("Alertmanager firing/resolved semantics = %#v / %#v", firing, resolved)
	}
	if firing.Annotations["xisnove_action"] != "open" || resolved.Annotations["xisnove_action"] != "recover" || firing.StartsAt == "" || resolved.StartsAt == "" {
		t.Fatalf("Alertmanager actions = %#v / %#v", firing, resolved)
	}

	listed, err := client.ListNotificationDeliveriesWithResponse(ctx, &sdk.ListNotificationDeliveriesParams{}, adminAuth)
	if err != nil || listed.JSON200 == nil || len(listed.JSON200.Items) != 8 {
		t.Fatalf("list deliveries: response=%#v error=%v", listed, err)
	}
	publicBodies = append(publicBodies, listed.Body)
	classes := map[string]int{}
	states := map[sdk.NotificationDeliveryState]int{}
	for _, delivery := range listed.JSON200.Items {
		states[delivery.State]++
		if delivery.LastErrorClass != nil {
			classes[*delivery.LastErrorClass]++
		}
		detail, detailErr := client.GetNotificationDeliveryWithResponse(ctx, delivery.Id, adminAuth)
		if detailErr != nil || detail.JSON200 == nil || len(detail.JSON200.Attempts) != 1 {
			t.Fatalf("get delivery %s: response=%#v error=%v", delivery.Id, detail, detailErr)
		}
		publicBodies = append(publicBodies, detail.Body)
	}
	if states[sdk.NotificationDeliveryStateDelivered] != 4 || states[sdk.NotificationDeliveryStateRetrying] != 4 {
		t.Fatalf("delivery states = %#v", states)
	}
	if classes["provider_retryable"] != 2 || classes["deadline_exceeded"] != 2 {
		t.Fatalf("retry classifications = %#v", classes)
	}
	channels, err := client.ListNotificationChannelsWithResponse(ctx, &sdk.ListNotificationChannelsParams{}, adminAuth)
	if err != nil || channels.JSON200 == nil || len(channels.JSON200.Items) != 4 {
		t.Fatalf("list channels: response=%#v error=%v", channels, err)
	}
	publicBodies = append(publicBodies, channels.Body)
	for name, id := range channelIDs {
		channel, channelErr := client.GetNotificationChannelWithResponse(ctx, id, adminAuth)
		if channelErr != nil || channel.JSON200 == nil || channel.JSON200.Name != name {
			t.Fatalf("get channel %s: response=%#v error=%v", name, channel, channelErr)
		}
		publicBodies = append(publicBodies, channel.Body)
	}
	for _, body := range publicBodies {
		assertNotificationSecretsAbsent(t, body, shoutrrrSecret, alertmanagerSecret)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT COALESCE(last_diagnostic, '') FROM notification_outbox
		UNION ALL
		SELECT COALESCE(diagnostic, '') FROM notification_delivery_attempts
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var diagnostic string
		if err := rows.Scan(&diagnostic); err != nil {
			t.Fatal(err)
		}
		assertNotificationSecretsAbsent(t, []byte(diagnostic), shoutrrrSecret, alertmanagerSecret)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func assertNotificationSecretsAbsent(t *testing.T, value []byte, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(string(value), secret) {
			t.Fatalf("notification secret leaked: %q", value)
		}
	}
}
