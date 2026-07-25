package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
	"github.com/araihu/xisnove/internal/adapters/postgres"
	"github.com/google/uuid"
)

func TestCompetingReplicaPoolsClaimEveryRunOnce(t *testing.T) {
	baseURL := os.Getenv("XISNOVE_TEST_POSTGRES_URL")
	if baseURL == "" {
		t.Skip("XISNOVE_TEST_POSTGRES_URL is not set")
	}
	ctx := context.Background()
	databaseURL := newPostgresTestSchema(t, baseURL)
	firstDB, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = firstDB.Close() })
	if err := postgres.Migrate(ctx, firstDB); err != nil {
		t.Fatal(err)
	}
	secondDB, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondDB.Close() })

	stores := []application.Store{
		postgres.NewStore(firstDB),
		postgres.NewStore(secondDB),
	}
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	location, err := domain.NewLocation(
		domain.LocationID(uuid.NewString()),
		"replica test",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	repositories := stores[0].Repositories()
	if err := repositories.Locations.Create(ctx, location); err != nil {
		t.Fatal(err)
	}
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID: domain.MonitorID(uuid.NewString()), Name: "replica monitor",
		Interval: time.Minute, Timeout: 5 * time.Second,
		FailureThreshold: 1, RecoveryThreshold: 1,
		HTTP: domain.HTTPProbe{
			Method:         "GET",
			URL:            "https://example.test/health",
			ExpectedStatus: []domain.StatusRange{{Min: 200, Max: 299}},
		},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.Monitors.Create(ctx, monitor); err != nil {
		t.Fatal(err)
	}

	const (
		runCount     = 50
		claimerCount = 8
	)
	agents := make([]domain.AgentID, 0, claimerCount)
	for index := range claimerCount {
		agent, err := domain.NewAgent(domain.NewAgentParams{
			ID: domain.AgentID(uuid.NewString()), LocationID: location.ID,
			Name:                 fmt.Sprintf("agent-%d", index),
			Capabilities:         []domain.AgentCapability{domain.CapabilityHTTP},
			CredentialGeneration: 1, CreatedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := repositories.Agents.Create(ctx, application.AgentRecord{
			Agent: agent, CredentialHash: []byte("credential-" + agent.ID),
		}); err != nil {
			t.Fatal(err)
		}
		agents = append(agents, agent.ID)
	}
	for index := range runCount {
		inserted, err := repositories.Runs.Insert(ctx, application.NewRunRecord{
			ID:        domain.CheckRunID(uuid.NewString()),
			MonitorID: monitor.ID, LocationID: location.ID,
			ScheduledFor: now.Add(time.Duration(index) * time.Millisecond),
			Probe:        monitor.Probe(), Timeout: monitor.Timeout,
		})
		if err != nil || !inserted {
			t.Fatalf("insert run %d = %v, %v", index, inserted, err)
		}
	}

	claimed := make(chan domain.CheckRunID, runCount)
	errs := make(chan error, claimerCount)
	var group sync.WaitGroup
	for index := range claimerCount {
		group.Add(1)
		go func() {
			defer group.Done()
			runRepository := stores[index%len(stores)].Repositories().Runs
			for {
				record, err := runRepository.ClaimProbe(ctx, application.ClaimRunParams{
					AgentID: agents[index],
					Capabilities: []domain.AgentCapability{
						domain.CapabilityHTTP,
					},
					LeaseTokenHash: []byte(fmt.Sprintf("lease-%d", index)),
					LeaseExpiresAt: now.Add(time.Hour),
					Now:            now.Add(time.Minute),
				})
				if errors.Is(err, application.ErrNotFound) {
					return
				}
				if err != nil {
					errs <- err
					return
				}
				claimed <- record.ID
			}
		}()
	}
	group.Wait()
	close(claimed)
	close(errs)
	for err := range errs {
		t.Errorf("claim error: %v", err)
	}
	seen := make(map[domain.CheckRunID]struct{}, runCount)
	for id := range claimed {
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("run %s was claimed more than once", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != runCount {
		t.Fatalf("claimed %d runs, want %d", len(seen), runCount)
	}
}

func newPostgresTestSchema(t *testing.T, baseURL string) string {
	t.Helper()
	ctx := context.Background()
	admin, err := postgres.Open(ctx, baseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "xisnove_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(
			context.Background(),
			"DROP SCHEMA "+schema+" CASCADE",
		); err != nil {
			t.Errorf("drop PostgreSQL test schema: %v", err)
		}
	})
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
