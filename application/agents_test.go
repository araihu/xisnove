package application_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
)

func TestEnrollmentTokenIsOneTimeAndLocationScoped(t *testing.T) {
	service, _ := newAgentServiceForTest(t)
	ctx := context.Background()
	enrollment, err := service.CreateEnrollmentToken(ctx, "l1", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	first, err := service.Enroll(ctx, application.EnrollAgentCommand{
		Token: enrollment.Token,
		Name:  "vps-1",
		Capabilities: []domain.AgentCapability{
			domain.CapabilityHTTP,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.LocationID != "l1" {
		t.Fatalf("LocationID = %s", first.LocationID)
	}

	_, err = service.Enroll(ctx, application.EnrollAgentCommand{
		Token: enrollment.Token,
		Name:  "vps-2",
		Capabilities: []domain.AgentCapability{
			domain.CapabilityHTTP,
		},
	})
	if !errors.Is(err, application.ErrInvalidEnrollmentToken) {
		t.Fatalf("error = %v", err)
	}
}

func TestEnrollmentWithCallerCredentialReplaysAfterLostResponse(t *testing.T) {
	service, _ := newAgentServiceForTest(t)
	ctx := context.Background()
	enrollment, err := service.CreateEnrollmentToken(ctx, "l1", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	command := application.EnrollAgentCommand{
		Token: enrollment.Token, Name: "edge-agent",
		Capabilities:   []domain.AgentCapability{domain.CapabilityHTTP},
		Credential:     "agent-caller-credential-01234567890123456789",
		IdempotencyKey: "chart-agent-enrollment-1",
	}
	first, err := service.Enroll(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.Enroll(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID != first.ID || replayed.Credential != command.Credential {
		t.Fatalf("replay = %#v, first = %#v", replayed, first)
	}
	if _, err := service.Authenticate(ctx, command.Credential); err != nil {
		t.Fatalf("authenticate caller credential: %v", err)
	}
	changed := command
	changed.Name = "different-agent"
	if _, err := service.Enroll(ctx, changed); !errors.Is(err, application.ErrIdempotencyKeyReused) {
		t.Fatalf("changed replay error = %v", err)
	}
}

func TestEnrollmentTokenClampsLifetime(t *testing.T) {
	tests := []struct {
		name string
		ttl  time.Duration
		want time.Duration
	}{
		{name: "default", ttl: 0, want: 15 * time.Minute},
		{name: "minimum", ttl: time.Second, want: time.Minute},
		{name: "maximum", ttl: 2 * time.Hour, want: time.Hour},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, _ := newAgentServiceForTest(t)
			enrollment, err := service.CreateEnrollmentToken(
				context.Background(),
				"l1",
				test.ttl,
			)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC)
			if got := enrollment.ExpiresAt.Sub(now); got != test.want {
				t.Fatalf("lifetime = %v, want %v", got, test.want)
			}
		})
	}
}

func TestAgentCredentialAuthenticatesAndHeartbeatUpdatesPresence(t *testing.T) {
	service, store := newAgentServiceForTest(t)
	ctx := context.Background()
	enrollment, err := service.CreateEnrollmentToken(ctx, "l1", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	enrolled, err := service.Enroll(ctx, application.EnrollAgentCommand{
		Token: enrollment.Token,
		Name:  "vps-1",
		Capabilities: []domain.AgentCapability{
			domain.CapabilityHTTP,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	principal, err := service.Authenticate(ctx, enrolled.Credential)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Kind != application.PrincipalAgent ||
		principal.SubjectID != string(enrolled.ID) ||
		principal.CredentialGeneration != 1 {
		t.Fatalf("Principal = %#v", principal)
	}
	err = service.Heartbeat(
		ctx,
		principal,
		1,
		"v0.1.0",
		[]domain.AgentCapability{domain.CapabilityHTTP},
	)
	if err != nil {
		t.Fatal(err)
	}

	record, err := store.Repositories().Agents.Get(ctx, enrolled.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Agent.Version != "v0.1.0" || record.Agent.LastSeenAt.IsZero() {
		t.Fatalf("Agent = %#v", record.Agent)
	}
}

func TestHeartbeatRejectsGenerationThatDoesNotMatchAuthenticatedCredential(t *testing.T) {
	service, store := newAgentServiceForTest(t)
	ctx := context.Background()
	enrollment, err := service.CreateEnrollmentToken(ctx, "l1", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	enrolled, err := service.Enroll(ctx, application.EnrollAgentCommand{
		Token: enrollment.Token,
		Name:  "vps-1",
		Capabilities: []domain.AgentCapability{
			domain.CapabilityHTTP,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := service.Authenticate(ctx, enrolled.Credential)
	if err != nil {
		t.Fatal(err)
	}
	repositories := store.Repositories()
	agents := &heartbeatCountingAgentRepository{AgentRepository: repositories.Agents}
	repositories.Agents = agents
	countingStore := &heartbeatCountingStore{repositories: repositories}
	service = application.NewAgentService(application.AgentServiceConfig{
		Store: countingStore,
		Now:   func() time.Time { return time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC) },
	})

	err = service.Heartbeat(
		ctx,
		principal,
		principal.CredentialGeneration+1,
		"v0.1.0",
		[]domain.AgentCapability{domain.CapabilityHTTP},
	)
	if !errors.Is(err, application.ErrInvalidCredentials) {
		t.Fatalf("error = %v", err)
	}
	if countingStore.transactions != 0 || agents.heartbeatCalls != 0 {
		t.Fatalf("heartbeat reached storage: transactions=%d updates=%d", countingStore.transactions, agents.heartbeatCalls)
	}
	record, err := store.Repositories().Agents.Get(ctx, enrolled.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Agent.Version != "" || !record.Agent.LastSeenAt.IsZero() {
		t.Fatalf("mismatched heartbeat updated agent: %#v", record.Agent)
	}
}

func newAgentServiceForTest(t *testing.T) (*application.AgentService, application.Store) {
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
	location, err := domain.NewLocation(
		"l1",
		"public",
		time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Repositories().Locations.Create(context.Background(), location); err != nil {
		t.Fatal(err)
	}
	nextID := 0
	service := application.NewAgentService(application.AgentServiceConfig{
		Store:  store,
		Tokens: &sequenceTokenIssuer{},
		Now: func() time.Time {
			return time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC)
		},
		NewID: func() string {
			nextID++
			return fmt.Sprintf("id-%d", nextID)
		},
	})
	return service, store
}

type sequenceTokenIssuer struct {
	next int
}

func (i *sequenceTokenIssuer) New() (application.IssuedToken, error) {
	i.next++
	raw := fmt.Sprintf("token-%d-01234567890123456789012345678901", i.next)
	hash := sha256.Sum256([]byte(raw))
	return application.IssuedToken{Raw: raw, Hash: hash[:]}, nil
}

func (*sequenceTokenIssuer) Hash(raw string) []byte {
	hash := sha256.Sum256([]byte(raw))
	return hash[:]
}

type heartbeatCountingStore struct {
	repositories application.Repositories
	transactions int
}

func (s *heartbeatCountingStore) View(ctx context.Context, callback func(context.Context, application.Repositories) error) error {
	return callback(ctx, s.repositories)
}

func (s *heartbeatCountingStore) Transact(ctx context.Context, callback func(context.Context, application.Repositories) error) error {
	s.transactions++
	return callback(ctx, s.repositories)
}

type heartbeatCountingAgentRepository struct {
	application.AgentRepository
	heartbeatCalls int
}

func (r *heartbeatCountingAgentRepository) UpdateHeartbeat(
	ctx context.Context,
	agentID domain.AgentID,
	credentialGeneration uint64,
	version string,
	capabilities []domain.AgentCapability,
	seenAt time.Time,
) (bool, error) {
	r.heartbeatCalls++
	return r.AgentRepository.UpdateHeartbeat(ctx, agentID, credentialGeneration, version, capabilities, seenAt)
}
