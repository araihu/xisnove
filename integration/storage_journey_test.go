package integration_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/araihu/xisnove/internal/adapters/database"
	"github.com/araihu/xisnove/internal/application"
	"github.com/araihu/xisnove/internal/domain"
)

const storageMatrixPassword = "correct horse battery staple"

var storageMatrixNow = time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)

func runStorageJourney(t *testing.T, harness *storageHarness) {
	t.Helper()
	ctx := context.Background()
	primary := harness.primary
	secondary := harness.secondary
	if err := primary.Ready(ctx); err != nil {
		t.Fatalf("primary readiness: %v", err)
	}
	if err := secondary.Ready(ctx); err != nil {
		t.Fatalf("secondary readiness: %v", err)
	}
	assertSchemaVersion(t, primary, 4)

	ids := &matrixIDs{}
	tokens := &matrixTokens{}
	passwords := matrixPasswords{}
	authNow := storageMatrixNow
	auth := application.NewAuthService(application.AuthServiceConfig{
		Store: primary.Store, Passwords: passwords, Tokens: tokens,
		SessionDuration: time.Hour, Now: func() time.Time { return authNow },
		NewID: ids.New,
	})
	if err := auth.BootstrapAdmin(ctx, "Admin@Example.com", storageMatrixPassword); err != nil {
		t.Fatal(err)
	}
	session, err := auth.CreateSession(ctx, "admin@example.com", storageMatrixPassword)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.AuthenticateSession(ctx, session.Token)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Kind != application.PrincipalAdmin || principal.SubjectID == "" {
		t.Fatalf("unexpected administrator principal: %#v", principal)
	}
	authNow = session.ExpiresAt.Add(500 * time.Millisecond)
	if _, err := auth.AuthenticateSession(ctx, session.Token); !errors.Is(err, application.ErrInvalidCredentials) {
		t.Fatalf("session remained active past fractional expiry boundary: %v", err)
	}
	authNow = storageMatrixNow

	configuration := application.NewConfigurationService(
		primary.Store,
		func() time.Time { return storageMatrixNow },
		ids.New,
	)
	location, err := configuration.CreateLocation(ctx, application.CreateLocationCommand{
		Name: "hybrid homelab",
	})
	if err != nil {
		t.Fatal(err)
	}
	monitors := make(map[domain.MonitorKind]application.ConfiguredMonitor, 3)
	monitorInputs := []application.CreateMonitorCommand{
		{
			Name: "public HTTP", LocationID: location.ID, RequiredLocation: true,
			Interval: 24 * time.Hour, Timeout: 5 * time.Second,
			FailureThreshold: 1, RecoveryThreshold: 1,
			Probe: domain.ProbeDefinition{Kind: domain.MonitorKindHTTP, HTTP: domain.HTTPProbe{
				Method: "GET", URL: "https://example.com/healthz",
				ExpectedStatus: []domain.StatusRange{{Min: 200, Max: 299}},
				BodyContains:   []string{"ok"},
			}},
		},
		{
			Name: "public TCP", LocationID: location.ID, RequiredLocation: true,
			Interval: 24 * time.Hour, Timeout: 5 * time.Second,
			FailureThreshold: 1, RecoveryThreshold: 1,
			Probe: domain.ProbeDefinition{Kind: domain.MonitorKindTCP, TCP: domain.TCPProbe{
				Host: "example.com", Port: 443, Send: []byte("hello"), Expect: []byte("world"),
			}},
		},
		{
			Name: "public DNS", LocationID: location.ID, RequiredLocation: true,
			Interval: 24 * time.Hour, Timeout: 5 * time.Second,
			FailureThreshold: 1, RecoveryThreshold: 1,
			Probe: domain.ProbeDefinition{Kind: domain.MonitorKindDNS, DNS: domain.DNSProbe{
				Resolver: "1.1.1.1:53", Name: "example.com", RecordType: "A",
				ExpectedValues: []string{"192.0.2.1"},
			}},
		},
	}
	for _, input := range monitorInputs {
		monitor, err := configuration.CreateMonitor(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		monitors[monitor.Kind] = monitor
	}

	capabilities := []domain.AgentCapability{
		domain.CapabilityHTTP,
		domain.CapabilityTCP,
		domain.CapabilityDNS,
	}
	agentNow := storageMatrixNow
	agents := application.NewAgentService(application.AgentServiceConfig{
		Store: primary.Store, Tokens: tokens,
		Now: func() time.Time { return agentNow }, NewID: ids.New,
	})
	enrollment, err := agents.CreateEnrollmentToken(ctx, location.ID, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	agentNow = enrollment.ExpiresAt.Add(500 * time.Millisecond)
	if _, err := agents.Enroll(ctx, application.EnrollAgentCommand{
		Token: enrollment.Token, Name: "expired-agent", Capabilities: capabilities,
	}); !errors.Is(err, application.ErrInvalidEnrollmentToken) {
		t.Fatalf("enrollment remained active past fractional expiry boundary: %v", err)
	}
	agentNow = storageMatrixNow
	enrolled, err := agents.Enroll(ctx, application.EnrollAgentCommand{
		Token: enrollment.Token, Name: "matrix-agent", Capabilities: capabilities,
	})
	if err != nil {
		t.Fatal(err)
	}
	agentPrincipal, err := agents.Authenticate(ctx, enrolled.Credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := agents.Heartbeat(
		ctx, agentPrincipal, 1, "v1.0.0-matrix", capabilities,
	); err != nil {
		t.Fatal(err)
	}
	persistedAgent, err := secondary.Store.Repositories().Agents.Get(ctx, enrolled.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persistedAgent.Agent.Version != "v1.0.0-matrix" || persistedAgent.Agent.LastSeenAt.IsZero() {
		t.Fatalf("heartbeat was not visible through secondary handle: %#v", persistedAgent.Agent)
	}

	scheduler := application.NewScheduler(primary.Store, ids.New)
	if count, err := scheduler.EnqueueDue(ctx, 10); err != nil || count != 3 {
		t.Fatalf("initial scheduler tick: inserted=%d error=%v", count, err)
	}
	if count, err := scheduler.EnqueueDue(ctx, 10); err != nil || count != 0 {
		t.Fatalf("idempotent scheduler tick: inserted=%d error=%v", count, err)
	}
	leaseService := application.NewLeaseService(application.LeaseServiceConfig{
		Store: secondary.Store, Tokens: tokens, LeaseDuration: 2 * time.Minute,
	})
	work := make(map[domain.MonitorKind]*application.ProbeWork, 3)
	for _, capability := range capabilities {
		leased, err := leaseService.LeaseProbe(ctx, enrolled.ID, []domain.AgentCapability{capability}, 0)
		if err != nil {
			t.Fatalf("lease %s probe through secondary handle: %v", capability, err)
		}
		work[leased.Probe.Kind] = leased
	}
	if len(work) != 3 {
		t.Fatalf("leased protocol kinds: %#v", work)
	}

	databaseNow, err := secondary.Store.Repositories().Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatalf("read database time through secondary handle: %v", err)
	}
	httpStatus := 503
	bodyPassed := false
	commands := []application.ProbeResultCommand{
		matrixResult(ids.New(), work[domain.MonitorKindHTTP], application.ProbeFailed, application.ProtocolTimings{
			DNS: 2 * time.Millisecond, Connect: 3 * time.Millisecond,
			TLS: 4 * time.Millisecond, FirstByte: 5 * time.Millisecond,
		}, []string{"HTTP/2 503"}, &httpStatus, &bodyPassed),
		matrixResult(ids.New(), work[domain.MonitorKindTCP], application.ProbePassed, application.ProtocolTimings{
			Connect: 7 * time.Millisecond, TLS: 8 * time.Millisecond,
		}, []string{"world"}, nil, nil),
		matrixResult(ids.New(), work[domain.MonitorKindDNS], application.ProbePassed, application.ProtocolTimings{
			DNS: 6 * time.Millisecond,
		}, []string{"192.0.2.1"}, nil, nil),
	}
	results := application.NewResultService(application.ResultServiceConfig{
		Store: secondary.Store, Tokens: tokens,
		Now: func() time.Time { return databaseNow }, NewID: ids.New,
		LeaseDuration: 2 * time.Minute,
	})
	acknowledgements, err := results.UploadBatch(ctx, enrolled.ID, commands)
	if err != nil {
		t.Fatalf("upload mixed protocol batch: %v", err)
	}
	assertAcknowledgements(t, acknowledgements, application.ResultAccepted)
	assertProtocolResults(t, primary.Store, work, commands)

	health := application.NewHealthService(primary.Store)
	assertHealthAndIncident(t, ctx, health, monitors[domain.MonitorKindHTTP].ID, domain.HealthDown, domain.IncidentCritical)
	assertHealthWithoutIncident(t, ctx, health, monitors[domain.MonitorKindTCP].ID, domain.HealthUp)
	assertHealthWithoutIncident(t, ctx, health, monitors[domain.MonitorKindDNS].ID, domain.HealthUp)

	acknowledgements, err = results.UploadBatch(ctx, enrolled.ID, commands)
	if err != nil {
		t.Fatal(err)
	}
	assertAcknowledgements(t, acknowledgements, application.ResultDuplicate)
	if count, err := scheduler.EnqueueDue(ctx, 10); err != nil || count != 0 {
		t.Fatalf("duplicate scheduler tick: inserted=%d error=%v", count, err)
	}
	assertTableCount(t, primary, "probe_results", 3)
	assertTableCount(t, primary, "check_runs", 3)
	assertTableCount(t, primary, "incident_events", 1)

	expiredRunID := domain.CheckRunID(ids.New())
	boundarySecond := databaseNow.Truncate(time.Minute).Add(30 * time.Second)
	inserted, err := primary.Store.Repositories().Runs.Insert(ctx, application.NewRunRecord{
		ID: expiredRunID, MonitorID: monitors[domain.MonitorKindHTTP].ID,
		LocationID: location.ID, ScheduledFor: boundarySecond,
		Probe: monitors[domain.MonitorKindHTTP].Probe(), Timeout: 5 * time.Second,
	})
	if err != nil || !inserted {
		t.Fatalf("insert expiry fixture: inserted=%t error=%v", inserted, err)
	}
	firstClaim, err := primary.Store.Repositories().Runs.ClaimProbe(ctx, application.ClaimRunParams{
		AgentID: enrolled.ID, Capabilities: []domain.AgentCapability{domain.CapabilityHTTP},
		LeaseTokenHash: tokens.Hash("expired-lease-1"),
		LeaseExpiresAt: boundarySecond.Add(time.Second), Now: boundarySecond.Add(500 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("claim expiry fixture through primary handle: %v", err)
	}
	secondClaim, err := secondary.Store.Repositories().Runs.ClaimProbe(ctx, application.ClaimRunParams{
		AgentID: enrolled.ID, Capabilities: []domain.AgentCapability{domain.CapabilityHTTP},
		LeaseTokenHash: tokens.Hash("expired-lease-2"),
		LeaseExpiresAt: boundarySecond.Add(2 * time.Minute), Now: boundarySecond.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("reclaim expiry fixture through secondary handle: %v", err)
	}
	if firstClaim.ID != expiredRunID || firstClaim.LeaseAttempt != 1 ||
		secondClaim.ID != expiredRunID || secondClaim.LeaseAttempt != 2 {
		t.Fatalf("unexpected lease reclaim: first=%#v second=%#v", firstClaim, secondClaim)
	}

	dnsMonitorID := monitors[domain.MonitorKindDNS].ID
	dnsHealth, err := primary.Store.Repositories().Health.GetLocation(ctx, dnsMonitorID, location.ID)
	if err != nil {
		t.Fatal(err)
	}
	dnsHealth.StaleAt = databaseNow.Truncate(time.Second)
	if err := primary.Store.Repositories().Health.UpsertLocation(ctx, dnsHealth); err != nil {
		t.Fatal(err)
	}
	staleness := application.NewStalenessService(secondary.Store, ids.New)
	if marked, err := staleness.MarkDue(ctx, 10); err != nil || marked != 1 {
		t.Fatalf("mark stale health: marked=%d error=%v", marked, err)
	}
	if marked, err := staleness.MarkDue(ctx, 10); err != nil || marked != 0 {
		t.Fatalf("idempotent stale-health tick: marked=%d error=%v", marked, err)
	}
	assertHealthAndIncident(t, ctx, health, dnsMonitorID, domain.HealthUnknown, domain.IncidentWarning)
	assertTableCount(t, primary, "incident_events", 2)

	rollbackID := domain.LocationID(ids.New())
	rollbackLocation, err := domain.NewLocation(rollbackID, "must roll back", databaseNow)
	if err != nil {
		t.Fatal(err)
	}
	rollbackFailure := errors.New("injected transaction failure")
	err = secondary.Store.WithinTx(ctx, func(repositories application.Repositories) error {
		if err := repositories.Locations.Create(ctx, rollbackLocation); err != nil {
			return err
		}
		return rollbackFailure
	})
	if !errors.Is(err, rollbackFailure) {
		t.Fatalf("transaction error = %v", err)
	}
	if _, err := primary.Store.Repositories().Locations.Get(ctx, rollbackID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("rolled-back location lookup error = %v", err)
	}

	reopened := harness.closeAndReopen(t)
	assertSchemaVersion(t, reopened, 4)
	assertTableCount(t, reopened, "admins", 1)
	assertTableCount(t, reopened, "sessions", 1)
	assertTableCount(t, reopened, "locations", 1)
	assertTableCount(t, reopened, "monitors", 3)
	assertTableCount(t, reopened, "agents", 1)
	assertTableCount(t, reopened, "check_runs", 4)
	assertTableCount(t, reopened, "probe_results", 3)
	assertTableCount(t, reopened, "incidents", 2)
	assertTableCount(t, reopened, "incident_events", 2)
	for _, monitor := range monitors {
		configured, err := application.NewConfigurationService(
			reopened.Store, func() time.Time { return storageMatrixNow }, ids.New,
		).GetMonitor(ctx, monitor.ID)
		if err != nil {
			t.Fatal(err)
		}
		if configured.Kind != monitor.Kind || configured.LocationID != location.ID || !configured.RequiredLocation {
			t.Fatalf("reopened monitor = %#v", configured)
		}
	}
	assertProtocolResults(t, reopened.Store, work, commands)
	reopenedHealth := application.NewHealthService(reopened.Store)
	assertHealthAndIncident(t, ctx, reopenedHealth, monitors[domain.MonitorKindHTTP].ID, domain.HealthDown, domain.IncidentCritical)
	assertHealthAndIncident(t, ctx, reopenedHealth, dnsMonitorID, domain.HealthUnknown, domain.IncidentWarning)
}

func matrixResult(
	id string,
	work *application.ProbeWork,
	outcome application.ProbeOutcome,
	timings application.ProtocolTimings,
	values []string,
	status *int,
	bodyPassed *bool,
) application.ProbeResultCommand {
	startedAt := work.ScheduledFor.Add(time.Millisecond)
	return application.ProbeResultCommand{
		ID: id, RunID: work.RunID, LeaseToken: work.LeaseToken,
		StartedAt: startedAt, FinishedAt: startedAt.Add(time.Millisecond),
		Outcome: outcome, Latency: time.Millisecond,
		ObservedStatus: status, BodyAssertionPassed: bodyPassed,
		ErrorCode: func() string {
			if outcome == application.ProbeFailed {
				return "status_mismatch"
			}
			return ""
		}(),
		DiagnosticSample: "storage matrix observation",
		ObservedValues:   values, ProtocolTimings: timings,
	}
}

func assertAcknowledgements(
	t *testing.T,
	acknowledgements []application.ResultAcknowledgement,
	want application.ResultStatus,
) {
	t.Helper()
	if len(acknowledgements) != 3 {
		t.Fatalf("acknowledgements = %#v", acknowledgements)
	}
	for _, acknowledgement := range acknowledgements {
		if acknowledgement.Status != want {
			t.Fatalf("acknowledgement = %#v, want status %s", acknowledgement, want)
		}
	}
}

func assertProtocolResults(
	t *testing.T,
	store application.Store,
	work map[domain.MonitorKind]*application.ProbeWork,
	commands []application.ProbeResultCommand,
) {
	t.Helper()
	ctx := context.Background()
	for _, command := range commands {
		persisted, err := store.Repositories().Results.GetByRun(ctx, command.RunID)
		if err != nil {
			t.Fatal(err)
		}
		kind := domain.MonitorKind("")
		for candidate, leased := range work {
			if leased.RunID == command.RunID {
				kind = candidate
				break
			}
		}
		if kind == "" || persisted.ID != command.ID || persisted.Passed != (command.Outcome == application.ProbePassed) ||
			persisted.ProtocolTimings != command.ProtocolTimings ||
			!slices.Equal(persisted.ObservedValues, command.ObservedValues) {
			t.Fatalf("persisted %s result = %#v, command = %#v", kind, persisted, command)
		}
		if !equalPointers(persisted.ObservedStatus, command.ObservedStatus) ||
			!equalPointers(persisted.BodyAssertionPassed, command.BodyAssertionPassed) {
			t.Fatalf("persisted %s protocol observations = %#v", kind, persisted)
		}
	}
}

func assertHealthAndIncident(
	t *testing.T,
	ctx context.Context,
	service *application.HealthService,
	monitorID domain.MonitorID,
	state domain.HealthState,
	severity domain.IncidentSeverity,
) {
	t.Helper()
	view, err := service.GetMonitorHealth(ctx, monitorID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Monitor.State != state || len(view.Locations) != 1 || view.Locations[0].State != state {
		t.Fatalf("health for %s = %#v", monitorID, view)
	}
	incident, err := service.GetActiveIncident(ctx, monitorID)
	if err != nil {
		t.Fatal(err)
	}
	if incident == nil || incident.State != state || incident.Severity != severity {
		t.Fatalf("incident for %s = %#v", monitorID, incident)
	}
}

func assertHealthWithoutIncident(
	t *testing.T,
	ctx context.Context,
	service *application.HealthService,
	monitorID domain.MonitorID,
	state domain.HealthState,
) {
	t.Helper()
	view, err := service.GetMonitorHealth(ctx, monitorID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Monitor.State != state || len(view.Locations) != 1 || view.Locations[0].State != state {
		t.Fatalf("health for %s = %#v", monitorID, view)
	}
	incident, err := service.GetActiveIncident(ctx, monitorID)
	if err != nil || incident != nil {
		t.Fatalf("active incident for %s = %#v, error=%v", monitorID, incident, err)
	}
}

func assertTableCount(t *testing.T, handle *database.Handle, table string, want int) {
	t.Helper()
	var count int
	if err := handle.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s row count = %d, want %d", table, count, want)
	}
}

func assertSchemaVersion(t *testing.T, handle *database.Handle, want int64) {
	t.Helper()
	if err := handle.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	var version int64
	if err := handle.DB.QueryRowContext(
		context.Background(),
		"SELECT COALESCE(MAX(version_id), 0) FROM schema_migrations WHERE is_applied",
	).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != want {
		t.Fatalf("schema version = %d, want %d", version, want)
	}
}

func equalPointers[T comparable](left, right *T) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

type matrixPasswords struct{}

func (matrixPasswords) Hash(password string) (string, error) { return "matrix:" + password, nil }
func (matrixPasswords) Verify(hash, password string) bool    { return hash == "matrix:"+password }

type matrixTokens struct {
	mu      sync.Mutex
	counter uint64
}

func (i *matrixTokens) New() (application.IssuedToken, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.counter++
	raw := fmt.Sprintf("matrix-token-%d", i.counter)
	return application.IssuedToken{Raw: raw, Hash: i.Hash(raw)}, nil
}

func (*matrixTokens) Hash(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

type matrixIDs struct {
	mu      sync.Mutex
	counter uint64
}

func (i *matrixIDs) New() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.counter++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", i.counter)
}
