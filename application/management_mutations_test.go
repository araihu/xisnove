package application_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

var managementDatabaseTime = time.Date(2035, 4, 5, 6, 7, 8, 9, time.FixedZone("skew", 4*60*60))

func TestManagementLocationMutationUsesDatabaseTimeIdempotencyAndAtomicAudit(t *testing.T) {
	fixture := newManagementMutationFixture(t)
	name := "  renamed edge  "
	updated, err := fixture.service.UpdateLocation(
		context.Background(), fixture.principal, managementID1, "location-key",
		application.UpdateLocationCommand{Name: &name},
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "renamed edge" || !updated.UpdatedAt.Equal(managementDatabaseTime.UTC()) {
		t.Fatalf("updated location = %#v", updated)
	}
	if len(fixture.repository.audits) != 1 || strings.Contains(string(fixture.repository.audits[0].Payload), "renamed edge") {
		t.Fatalf("audit leaks values or count is wrong: %#v", fixture.repository.audits)
	}

	replayed, err := fixture.service.UpdateLocation(
		context.Background(), fixture.principal, managementID1, "location-key",
		application.UpdateLocationCommand{Name: &name},
	)
	if err != nil || replayed.Name != updated.Name || len(fixture.repository.audits) != 1 {
		t.Fatalf("replay = %#v, %v, audits=%d", replayed, err, len(fixture.repository.audits))
	}
	other := "different"
	_, err = fixture.service.UpdateLocation(
		context.Background(), fixture.principal, managementID1, "location-key",
		application.UpdateLocationCommand{Name: &other},
	)
	if !errors.Is(err, application.ErrIdempotencyKeyReused) {
		t.Fatalf("mismatched replay error = %v", err)
	}
	name = updated.Name
	if _, err := fixture.service.UpdateLocation(
		context.Background(), fixture.principal, managementID1, "location-noop-key",
		application.UpdateLocationCommand{Name: &name},
	); err != nil || len(fixture.repository.audits) != 1 {
		t.Fatalf("no-op update error=%v audits=%d", err, len(fixture.repository.audits))
	}

	fixture.repository.auditErr = errors.New("audit unavailable")
	enabled := false
	_, err = fixture.service.UpdateLocation(
		context.Background(), fixture.principal, managementID1, "rollback-key",
		application.UpdateLocationCommand{Enabled: &enabled},
	)
	if err == nil || !fixture.repository.locations[managementID1].Enabled {
		t.Fatalf("audit rollback error=%v location=%#v", err, fixture.repository.locations[managementID1])
	}
	if _, exists := fixture.repository.idempotency[managementIdempotencyIdentity(fixture.principal, "updateLocation", "rollback-key")]; exists {
		t.Fatal("rolled-back mutation retained idempotency reservation")
	}
}

func TestManagementDisableNoOpsDoNotAuditAndEffectiveDisableRollsBack(t *testing.T) {
	fixture := newManagementMutationFixture(t)
	if err := fixture.service.DisableLocation(context.Background(), fixture.principal, managementID1); err != nil {
		t.Fatal(err)
	}
	if fixture.repository.locations[managementID1].Enabled || len(fixture.repository.audits) != 1 {
		t.Fatalf("effective disable state=%#v audits=%d", fixture.repository.locations[managementID1], len(fixture.repository.audits))
	}
	if err := fixture.service.DisableLocation(context.Background(), fixture.principal, managementID1); err != nil {
		t.Fatal(err)
	}
	if len(fixture.repository.audits) != 1 {
		t.Fatalf("no-op disable audits=%d", len(fixture.repository.audits))
	}

	monitor := fixture.repository.monitors[managementID2]
	monitor.Monitor.Enabled = true
	fixture.repository.monitors[managementID2] = monitor
	fixture.repository.auditErr = errors.New("audit unavailable")
	err := fixture.service.DisableMonitor(context.Background(), fixture.principal, managementID2)
	if err == nil || !fixture.repository.monitors[managementID2].Monitor.Enabled {
		t.Fatalf("monitor rollback error=%v monitor=%#v", err, fixture.repository.monitors[managementID2])
	}
}

func TestManagementMonitorReplacementPreservesIdentityAndAssignmentAtomically(t *testing.T) {
	fixture := newManagementMutationFixture(t)
	command := validMonitorReplacement()
	command.LocationID = managementID3
	command.RequiredLocation = false
	command.Enabled = false
	replaced, err := fixture.service.UpdateMonitor(
		context.Background(), fixture.principal, managementID2, "monitor-key", command,
	)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.ID != managementID2 || !replaced.CreatedAt.Equal(fixture.repository.monitors[managementID2].Monitor.CreatedAt) ||
		!replaced.UpdatedAt.Equal(managementDatabaseTime.UTC()) || replaced.LocationID != managementID3 ||
		replaced.Enabled {
		t.Fatalf("replacement = %#v", replaced)
	}
	if len(fixture.repository.audits) != 1 {
		t.Fatalf("replacement audits=%d", len(fixture.repository.audits))
	}

	invalid := command
	invalid.Timeout = invalid.Interval
	_, err = fixture.service.UpdateMonitor(
		context.Background(), fixture.principal, managementID2, "invalid-monitor-key", invalid,
	)
	var validation *application.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("invalid replacement error=%v", err)
	}

	rollback := newManagementMutationFixture(t)
	rollback.repository.auditErr = errors.New("audit unavailable")
	_, err = rollback.service.UpdateMonitor(
		context.Background(), rollback.principal, managementID2, "monitor-rollback-key", command,
	)
	stored := rollback.repository.monitors[managementID2]
	if err == nil || stored.LocationID != managementID1 || stored.Monitor.Name != "existing" {
		t.Fatalf("replacement rollback error=%v stored=%#v", err, stored)
	}
}

func TestManagementAgentUpdateRevokesPermanentlyAndUsesOneAudit(t *testing.T) {
	fixture := newManagementMutationFixture(t)
	name := "renamed agent"
	capabilities := []domain.AgentCapability{
		domain.CapabilityDNS,
		domain.CapabilityHTTP,
		domain.CapabilityKubernetesDiscovery,
		domain.CapabilityKubernetesWatch,
	}
	enabled := false
	updated, err := fixture.service.UpdateAgent(
		context.Background(), fixture.principal, managementID3, "agent-key",
		application.UpdateAgentCommand{Name: &name, Capabilities: &capabilities, Enabled: &enabled},
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.RevokedAt == nil || !updated.RevokedAt.Equal(managementDatabaseTime.UTC()) ||
		updated.Name != name || !slices.Equal(updated.Capabilities, capabilities) {
		t.Fatalf("updated agent=%#v", updated)
	}
	if len(fixture.repository.audits) != 1 || fixture.repository.audits[0].Kind != "agent.revoked" {
		t.Fatalf("agent audit=%#v", fixture.repository.audits)
	}
	if !fixture.repository.allGenerationsRevoked {
		t.Fatal("enabled=false did not revoke every credential generation")
	}

	enabled = true
	_, err = fixture.service.UpdateAgent(
		context.Background(), fixture.principal, managementID3, "resurrect-key",
		application.UpdateAgentCommand{Enabled: &enabled},
	)
	if !errors.Is(err, application.ErrConflict) {
		t.Fatalf("resurrection error=%v", err)
	}
	if err := fixture.service.RevokeAgent(context.Background(), fixture.principal, managementID3); err != nil {
		t.Fatal(err)
	}
	if len(fixture.repository.audits) != 1 {
		t.Fatalf("repeated revoke audits=%d", len(fixture.repository.audits))
	}

	direct := newManagementMutationFixture(t)
	direct.repository.auditErr = errors.New("audit unavailable")
	if err := direct.service.RevokeAgent(context.Background(), direct.principal, managementID3); err == nil ||
		direct.repository.agents[managementID3].RevokedAt != nil || direct.repository.allGenerationsRevoked {
		t.Fatalf("direct revoke rollback error=%v agent=%#v", err, direct.repository.agents[managementID3])
	}
}

func TestManagementCredentialRotationReservesBeforeMintAndNeverReplaysPlaintext(t *testing.T) {
	fixture := newManagementMutationFixture(t)
	fixture.issuer.onNew = func() {
		identity := managementIdempotencyIdentity(fixture.principal, "rotateAgentCredential", "rotation-key")
		if _, reserved := fixture.repository.idempotency[identity]; !reserved {
			t.Error("credential was minted before the idempotency reservation")
		}
	}
	credential, err := fixture.service.RotateAgentCredential(
		context.Background(), fixture.principal, managementID3, "rotation-key",
	)
	if err != nil {
		t.Fatal(err)
	}
	if credential.Credential != "secret-credential" || credential.CredentialGeneration != 2 || fixture.issuer.calls != 1 {
		t.Fatalf("credential=%#v issuer calls=%d", credential, fixture.issuer.calls)
	}
	stored := fixture.repository.credentials[2]
	if string(stored.CredentialHash) == credential.Credential || !stored.CreatedAt.Equal(managementDatabaseTime.UTC()) {
		t.Fatalf("stored credential=%#v", stored)
	}
	encodedAudit, _ := json.Marshal(fixture.repository.audits)
	if strings.Contains(string(encodedAudit), credential.Credential) || strings.Contains(string(encodedAudit), string(stored.CredentialHash)) {
		t.Fatalf("credential leaked in audit: %s", encodedAudit)
	}

	_, err = fixture.service.RotateAgentCredential(
		context.Background(), fixture.principal, managementID3, "rotation-key",
	)
	if !errors.Is(err, application.ErrCredentialAlreadyIssued) || fixture.issuer.calls != 1 {
		t.Fatalf("rotation replay error=%v issuer calls=%d", err, fixture.issuer.calls)
	}
	_, err = fixture.service.RotateAgentCredential(
		context.Background(), fixture.principal, managementID3, "second-rotation-key",
	)
	if !errors.Is(err, application.ErrConflict) || fixture.issuer.calls != 2 {
		t.Fatalf("overlap conflict error=%v issuer calls=%d", err, fixture.issuer.calls)
	}
}

func TestManagementCredentialRotationLoserReturnsAlreadyIssuedWithoutMinting(t *testing.T) {
	fixture := newManagementMutationFixture(t)
	requestHash, err := application.CanonicalRequestFingerprint(struct {
		AgentID domain.AgentID
	}{managementID3})
	if err != nil {
		t.Fatal(err)
	}
	key := "loser-key"
	fixture.repository.idempotency[managementIdempotencyIdentity(fixture.principal, "rotateAgentCredential", key)] = port.IdempotencyRecord{
		PrincipalID: fixture.principal.CredentialID, OperationID: "rotateAgentCredential", Key: key,
		RequestHash: requestHash, ResourceKind: "agent-credential", ResourceID: managementID3,
		CreatedAt: managementDatabaseTime.UTC(), ExpiresAt: managementDatabaseTime.UTC().Add(time.Hour),
	}
	_, err = fixture.service.RotateAgentCredential(context.Background(), fixture.principal, managementID3, key)
	if !errors.Is(err, application.ErrCredentialAlreadyIssued) || fixture.issuer.calls != 0 {
		t.Fatalf("loser error=%v issuer calls=%d", err, fixture.issuer.calls)
	}
}

func TestManagementCredentialRotationAuditFailureRollsBackGenerationAndReservation(t *testing.T) {
	fixture := newManagementMutationFixture(t)
	fixture.repository.auditErr = errors.New("audit unavailable")
	_, err := fixture.service.RotateAgentCredential(
		context.Background(), fixture.principal, managementID3, "rotation-rollback-key",
	)
	if err == nil {
		t.Fatal("rotation unexpectedly succeeded")
	}
	if fixture.repository.agents[managementID3].CredentialGeneration != 1 || len(fixture.repository.credentials) != 1 {
		t.Fatalf("rotation did not roll back: agent=%#v credentials=%#v", fixture.repository.agents[managementID3], fixture.repository.credentials)
	}
	if _, exists := fixture.repository.idempotency[managementIdempotencyIdentity(fixture.principal, "rotateAgentCredential", "rotation-rollback-key")]; exists {
		t.Fatal("rotation reservation survived rollback")
	}
}

func TestManagementCredentialGenerationRevokeMapsOverlapOutcomesAndAuditsOnlyEffectiveWrite(t *testing.T) {
	tests := []struct {
		name    string
		outcome port.CredentialGenerationRevokeOutcome
		wantErr error
		audits  int
	}{
		{"revoked", port.CredentialGenerationRevoked, nil, 1},
		{"repeat", port.CredentialGenerationAlreadyRevoked, nil, 0},
		{"missing", port.CredentialGenerationNotFound, application.ErrNotFound, 0},
		{"current", port.CredentialGenerationCurrent, application.ErrConflict, 0},
		{"replacement-unobserved", port.CredentialGenerationReplacementUnobserved, application.ErrConflict, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newManagementMutationFixture(t)
			fixture.repository.revokeGenerationOutcome = test.outcome
			err := fixture.service.RevokeAgentCredentialGeneration(context.Background(), fixture.principal, managementID3, 1)
			if !errors.Is(err, test.wantErr) || len(fixture.repository.audits) != test.audits {
				t.Fatalf("error=%v audits=%d", err, len(fixture.repository.audits))
			}
		})
	}
}

func TestManagementMutationsRejectInvalidPrincipalIdentity(t *testing.T) {
	fixture := newManagementMutationFixture(t)
	invalid := fixture.principal
	invalid.CredentialKind = application.CredentialAgent
	if err := fixture.service.DisableLocation(context.Background(), invalid, managementID1); !errors.Is(err, application.ErrInvalidCredentials) {
		t.Fatalf("invalid credential identity error=%v", err)
	}
	agent := application.Principal{
		Kind: application.PrincipalAgent, SubjectID: managementID3,
		CredentialKind: application.CredentialAgent, CredentialID: managementID3,
	}
	if err := fixture.service.RevokeAgent(context.Background(), agent, managementID3); !errors.Is(err, application.ErrForbidden) {
		t.Fatalf("agent principal error=%v", err)
	}
}

type managementMutationFixture struct {
	service    *application.ManagementService
	repository *managementMutationRepository
	issuer     *managementTokenIssuer
	principal  application.Principal
}

func newManagementMutationFixture(t *testing.T) *managementMutationFixture {
	t.Helper()
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	location := domain.Location{ID: managementID1, Name: "edge", Enabled: true, CreatedAt: created, UpdatedAt: created}
	monitor, err := newTestMonitor(managementID2, created)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := domain.NewAgent(domain.NewAgentParams{
		ID: managementID3, LocationID: managementID1, Name: "agent",
		Capabilities:         []domain.AgentCapability{domain.CapabilityHTTP},
		CredentialGeneration: 1, CreatedAt: created,
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := &managementMutationRepository{
		now: managementDatabaseTime,
		locations: map[domain.LocationID]domain.Location{
			location.ID: location,
			managementID3: {
				ID: managementID3, Name: "alternate", Enabled: true, CreatedAt: created, UpdatedAt: created,
			},
		},
		monitors: map[domain.MonitorID]port.MonitorRecord{monitor.ID: {
			Monitor: monitor, LocationID: managementID1, RequiredLocation: true,
		}},
		agents: map[domain.AgentID]domain.Agent{agent.ID: agent},
		credentials: map[uint64]port.AgentCredentialRecord{1: {
			AgentID: agent.ID, Generation: 1, CredentialHash: []byte("old-hash"), CreatedAt: created,
		}},
		idempotency:             make(map[string]port.IdempotencyRecord),
		revokeGenerationOutcome: port.CredentialGenerationRevoked,
	}
	issuer := &managementTokenIssuer{}
	principal := application.Principal{
		Kind: application.PrincipalAdmin, SubjectID: "admin-1",
		CredentialKind: application.CredentialSession, CredentialID: "session-1",
	}
	service := application.NewManagementService(application.ManagementServiceConfig{
		Store: repository, Tokens: issuer, NewID: func() string { return "audit-id" },
	})
	return &managementMutationFixture{
		service: service, repository: repository, issuer: issuer, principal: principal,
	}
}

func validMonitorReplacement() application.ReplaceMonitorCommand {
	return application.ReplaceMonitorCommand{
		CreateMonitorCommand: application.CreateMonitorCommand{
			Name: "replacement", Description: "redacted details", Labels: map[string]string{"tier": "edge"},
			DisplayOrder: 8, Public: true, LocationID: managementID1, RequiredLocation: true,
			Interval: time.Minute, Timeout: 5 * time.Second, FailureThreshold: 2, RecoveryThreshold: 3,
			Probe: domain.ProbeDefinition{Kind: domain.MonitorKindHTTP, HTTP: domain.HTTPProbe{
				Method: "GET", URL: "https://example.test/health", Headers: map[string]string{"Authorization": "secret"},
				ExpectedStatus: []domain.StatusRange{{Min: 200, Max: 299}},
			}},
		},
		Enabled: true,
	}
}

func newTestMonitor(id domain.MonitorID, created time.Time) (domain.Monitor, error) {
	command := validMonitorReplacement().CreateMonitorCommand
	return domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID: id, Name: "existing", Description: command.Description, Labels: command.Labels,
		DisplayOrder: 1, Interval: command.Interval, Timeout: command.Timeout,
		FailureThreshold: command.FailureThreshold, RecoveryThreshold: command.RecoveryThreshold,
		HTTP: command.Probe.HTTP, CreatedAt: created,
	})
}

type managementTokenIssuer struct {
	calls int
	onNew func()
}

func (i *managementTokenIssuer) New() (application.IssuedToken, error) {
	i.calls++
	if i.onNew != nil {
		i.onNew()
	}
	return application.IssuedToken{Raw: "secret-credential", Hash: i.Hash("secret-credential")}, nil
}

func (*managementTokenIssuer) Hash(raw string) []byte {
	hash := sha256.Sum256([]byte(raw))
	return hash[:]
}

type managementMutationRepository struct {
	now                     time.Time
	locations               map[domain.LocationID]domain.Location
	monitors                map[domain.MonitorID]port.MonitorRecord
	agents                  map[domain.AgentID]domain.Agent
	credentials             map[uint64]port.AgentCredentialRecord
	idempotency             map[string]port.IdempotencyRecord
	audits                  []port.AuditEventRecord
	auditErr                error
	allGenerationsRevoked   bool
	revokeGenerationOutcome port.CredentialGenerationRevokeOutcome
}

func (r *managementMutationRepository) repositories() port.Repositories {
	return port.Repositories{
		Idempotency: r, Runs: managementRunRepository{r}, Audit: r, Management: r, ManagementCommands: r,
	}
}

func (r *managementMutationRepository) View(ctx context.Context, fn func(context.Context, port.Repositories) error) error {
	return fn(ctx, r.repositories())
}

func (r *managementMutationRepository) Transact(ctx context.Context, fn func(context.Context, port.Repositories) error) error {
	snapshot := r.clone()
	if err := fn(ctx, r.repositories()); err != nil {
		*r = *snapshot
		return err
	}
	return nil
}

func (r *managementMutationRepository) clone() *managementMutationRepository {
	clone := *r
	clone.locations = make(map[domain.LocationID]domain.Location, len(r.locations))
	for key, value := range r.locations {
		clone.locations[key] = value
	}
	clone.monitors = make(map[domain.MonitorID]port.MonitorRecord, len(r.monitors))
	for key, value := range r.monitors {
		clone.monitors[key] = value
	}
	clone.agents = make(map[domain.AgentID]domain.Agent, len(r.agents))
	for key, value := range r.agents {
		value.Capabilities = slices.Clone(value.Capabilities)
		clone.agents[key] = value
	}
	clone.credentials = make(map[uint64]port.AgentCredentialRecord, len(r.credentials))
	for key, value := range r.credentials {
		value.CredentialHash = slices.Clone(value.CredentialHash)
		clone.credentials[key] = value
	}
	clone.idempotency = make(map[string]port.IdempotencyRecord, len(r.idempotency))
	for key, value := range r.idempotency {
		clone.idempotency[key] = value
	}
	clone.audits = slices.Clone(r.audits)
	return &clone
}

type managementRunRepository struct{ owner *managementMutationRepository }

func (r managementRunRepository) DatabaseNow(context.Context) (time.Time, error) {
	return r.owner.now, nil
}

func (managementRunRepository) Insert(context.Context, port.NewRunRecord) (bool, error) {
	return false, nil
}

func (managementRunRepository) ClaimProbe(context.Context, port.ClaimRunParams) (port.RunRecord, error) {
	return port.RunRecord{}, port.ErrNotFound
}

func (managementRunRepository) Get(context.Context, domain.CheckRunID) (port.RunRecord, error) {
	return port.RunRecord{}, port.ErrNotFound
}

func (managementRunRepository) Resolve(context.Context, domain.CheckRunID, domain.AgentID, []byte, time.Time) (bool, error) {
	return false, nil
}

func (r *managementMutationRepository) Get(ctx context.Context, principal, operation, key string, now time.Time) (port.IdempotencyRecord, error) {
	record, ok := r.idempotency[principal+"\x00"+operation+"\x00"+key]
	if !ok || !record.ExpiresAt.After(now) {
		return port.IdempotencyRecord{}, port.ErrNotFound
	}
	return record, nil
}

func (r *managementMutationRepository) Create(_ context.Context, record port.IdempotencyRecord) error {
	key := record.PrincipalID + "\x00" + record.OperationID + "\x00" + record.Key
	if existing, ok := r.idempotency[key]; ok && existing.ExpiresAt.After(record.CreatedAt) {
		return port.ErrConflict
	}
	r.idempotency[key] = record
	return nil
}

func (*managementMutationRepository) DeleteExpired(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func (r *managementMutationRepository) Append(_ context.Context, record port.AuditEventRecord) error {
	if r.auditErr != nil {
		return r.auditErr
	}
	r.audits = append(r.audits, record)
	return nil
}

func (*managementMutationRepository) ListByIncident(context.Context, domain.IncidentID) ([]port.AuditEventRecord, error) {
	return nil, nil
}

func (r *managementMutationRepository) GetLocation(_ context.Context, id domain.LocationID) (domain.Location, error) {
	value, ok := r.locations[id]
	if !ok {
		return domain.Location{}, port.ErrNotFound
	}
	return value, nil
}

func (*managementMutationRepository) ListLocations(context.Context, port.StringKeysetRequest) ([]domain.Location, error) {
	return nil, nil
}

func (r *managementMutationRepository) GetMonitor(_ context.Context, id domain.MonitorID) (port.MonitorRecord, error) {
	value, ok := r.monitors[id]
	if !ok {
		return port.MonitorRecord{}, port.ErrNotFound
	}
	return value, nil
}

func (*managementMutationRepository) ListMonitors(context.Context, port.IntKeysetRequest) ([]port.MonitorRecord, error) {
	return nil, nil
}

func (r *managementMutationRepository) GetAgent(_ context.Context, id domain.AgentID) (domain.Agent, error) {
	value, ok := r.agents[id]
	if !ok {
		return domain.Agent{}, port.ErrNotFound
	}
	value.Capabilities = slices.Clone(value.Capabilities)
	return value, nil
}

func (*managementMutationRepository) ListAgents(context.Context, port.StringKeysetRequest) ([]domain.Agent, error) {
	return nil, nil
}

func (*managementMutationRepository) GetIncident(context.Context, domain.IncidentID) (domain.Incident, error) {
	return domain.Incident{}, port.ErrNotFound
}

func (*managementMutationRepository) ListIncidents(context.Context, port.IncidentListRequest) ([]domain.Incident, error) {
	return nil, nil
}

func (*managementMutationRepository) ListIncidentEvents(context.Context, domain.IncidentID, port.TimeKeysetRequest) ([]domain.IncidentEvent, error) {
	return nil, nil
}

func (r *managementMutationRepository) ReplaceLocation(_ context.Context, value domain.Location) (bool, error) {
	if _, ok := r.locations[value.ID]; !ok {
		return false, nil
	}
	r.locations[value.ID] = value
	return true, nil
}

func (r *managementMutationRepository) DisableLocation(_ context.Context, id domain.LocationID, now time.Time) (bool, error) {
	value, ok := r.locations[id]
	if !ok || !value.Enabled {
		return false, nil
	}
	value.Enabled, value.UpdatedAt = false, now
	r.locations[id] = value
	return true, nil
}

func (r *managementMutationRepository) ReplaceMonitor(_ context.Context, value port.MonitorRecord) (bool, error) {
	if _, ok := r.monitors[value.Monitor.ID]; !ok {
		return false, nil
	}
	r.monitors[value.Monitor.ID] = value
	return true, nil
}

func (r *managementMutationRepository) DisableMonitor(_ context.Context, id domain.MonitorID, now time.Time) (bool, error) {
	value, ok := r.monitors[id]
	if !ok || !value.Monitor.Enabled {
		return false, nil
	}
	value.Monitor.Enabled, value.Monitor.UpdatedAt = false, now
	r.monitors[id] = value
	return true, nil
}

func (r *managementMutationRepository) UpdateAgent(_ context.Context, value domain.Agent) (bool, error) {
	if _, ok := r.agents[value.ID]; !ok {
		return false, nil
	}
	r.agents[value.ID] = cloneTestAgent(value)
	return true, nil
}

func (r *managementMutationRepository) RevokeAgent(_ context.Context, id domain.AgentID, now time.Time) (bool, error) {
	value, ok := r.agents[id]
	if !ok || value.RevokedAt != nil {
		return false, nil
	}
	value.RevokedAt, value.UpdatedAt = &now, now
	r.agents[id] = value
	r.allGenerationsRevoked = true
	return true, nil
}

func (r *managementMutationRepository) CreateAgentCredentialGeneration(_ context.Context, command port.CreateAgentCredentialGenerationCommand) (bool, error) {
	agent, ok := r.agents[command.Credential.AgentID]
	if !ok || agent.CredentialGeneration != command.ExpectedCurrentGeneration || len(r.credentials) >= 2 {
		return false, nil
	}
	r.credentials[command.Credential.Generation] = command.Credential
	agent.CredentialGeneration = command.Credential.Generation
	agent.UpdatedAt = command.Credential.CreatedAt
	r.agents[agent.ID] = agent
	return true, nil
}

func (r *managementMutationRepository) GetAgentCredentialGeneration(_ context.Context, _ domain.AgentID, generation uint64) (port.AgentCredentialRecord, error) {
	value, ok := r.credentials[generation]
	if !ok {
		return port.AgentCredentialRecord{}, port.ErrNotFound
	}
	return value, nil
}

func (r *managementMutationRepository) RevokeAgentCredentialGeneration(_ context.Context, _ domain.AgentID, _ uint64, _ time.Time) (port.CredentialGenerationRevokeOutcome, error) {
	return r.revokeGenerationOutcome, nil
}

func managementIdempotencyIdentity(principal application.Principal, operation, key string) string {
	return principal.CredentialID + "\x00" + operation + "\x00" + key
}

func cloneTestAgent(value domain.Agent) domain.Agent {
	value.Capabilities = slices.Clone(value.Capabilities)
	return value
}
