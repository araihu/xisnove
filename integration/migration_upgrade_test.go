package integration_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	postgresmigrations "github.com/araihu/xisnove/db/migrations/postgres"
	sqlitemigrations "github.com/araihu/xisnove/db/migrations/sqlite"
	"github.com/araihu/xisnove/domain"
	"github.com/araihu/xisnove/internal/adapters/database"
	postgresstore "github.com/araihu/xisnove/internal/adapters/postgres"
	postgrescontainer "github.com/araihu/xisnove/internal/testsupport/postgrescontainer"
	"github.com/pressly/goose/v3"
)

func TestMigrationUpgradeAcrossLocalProfiles(t *testing.T) {
	t.Parallel()

	for _, profile := range []database.Profile{
		database.ProfileSQLite,
		database.ProfileTursoLocal,
	} {
		profile := profile
		t.Run(string(profile), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			handle, err := database.Open(ctx, database.Config{
				Profile: profile,
				URL:     filepath.Join(t.TempDir(), "upgrade.db"),
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = handle.Close() })
			provider, err := goose.NewProvider(
				goose.DialectSQLite3,
				handle.DB,
				sqlitemigrations.Files,
				goose.WithTableName("schema_migrations"),
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.UpTo(ctx, 1); err != nil {
				t.Fatal(err)
			}
			seedVersionOneMonitor(t, handle, false)
			if _, err := provider.UpTo(ctx, 3); err != nil {
				t.Fatal(err)
			}
			seedVersionThreeIncident(t, handle)
			if _, err := provider.UpTo(ctx, 6); err != nil {
				t.Fatal(err)
			}
			seedVersionSixAgent(t, handle, false)
			if err := handle.Ready(ctx); err == nil {
				t.Fatal("version 6 database reported ready for version 7 binary")
			}
			if err := handle.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			assertUpgradedMonitor(t, handle)
			assertUpgradedIncident(t, handle)
			assertCurrentAgentWriterPopulatesUpdatedAt(t, handle)
			assertAgentUpdatedAtRejectsNull(t, handle, false)
			assertUpgradedAgentCredential(t, handle)
		})
	}
}

func TestMigrationUpgradePostgres(t *testing.T) {
	baseURL := postgrescontainer.URL(t, os.Getenv("XISNOVE_TEST_POSTGRES_URL"))
	ctx := context.Background()
	admin, err := postgresstore.Open(ctx, baseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "xisnove_upgrade_" + randomHex(t)
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	handle, err := database.Open(ctx, database.Config{
		Profile: database.ProfilePostgres,
		URL:     parsed.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		handle.DB,
		postgresmigrations.Files,
		goose.WithTableName("schema_migrations"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	seedVersionOneMonitor(t, handle, true)
	if _, err := provider.UpTo(ctx, 3); err != nil {
		t.Fatal(err)
	}
	seedVersionThreeIncident(t, handle)
	if _, err := provider.UpTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
	seedVersionSixAgent(t, handle, true)
	if err := handle.Ready(ctx); err == nil {
		t.Fatal("version 6 database reported ready for version 7 binary")
	}
	if err := handle.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	assertUpgradedMonitor(t, handle)
	assertUpgradedIncident(t, handle)
	assertCurrentAgentWriterPopulatesUpdatedAt(t, handle)
	assertAgentUpdatedAtRejectsNull(t, handle, true)
	assertUpgradedAgentCredential(t, handle)
	assertPostgresAgentCredentialDowngrade(t, handle, provider)
}

func seedVersionSixAgent(t *testing.T, handle *database.Handle, postgres bool) {
	t.Helper()
	credentialHash := "X'01020304'"
	if postgres {
		credentialHash = `decode('01020304', 'hex')`
	}
	_, err := handle.DB.ExecContext(context.Background(), `
		INSERT INTO agents (
			id, location_id, name, credential_hash, credential_generation,
			capabilities_json, version, last_seen_at, revoked_at, created_at
		) VALUES (
			'00000000-0000-4000-8000-000000000105',
			'00000000-0000-4000-8000-000000000101',
			'upgrade-agent', `+credentialHash+`, 3, '["http"]', 'v6',
			'2026-07-25T12:03:00Z', NULL, '2026-07-25T12:02:00Z'
		);
		INSERT INTO agents (
			id, location_id, name, credential_hash, credential_generation,
			capabilities_json, version, last_seen_at, revoked_at, created_at
		) VALUES (
			'00000000-0000-4000-8000-000000000106',
			'00000000-0000-4000-8000-000000000101',
			'revoked-upgrade-agent', `+credentialHashLiteral(postgres, "05060708")+`, 2,
			'["http"]', 'v6', '2026-07-25T12:04:00Z',
			'2026-07-25T12:05:00Z', '2026-07-25T12:02:00Z'
		)
	`)
	if err != nil {
		t.Fatal(err)
	}
}

func assertUpgradedAgentCredential(t *testing.T, handle *database.Handle) {
	t.Helper()
	var agentUpdatedAt string
	if err := handle.DB.QueryRowContext(context.Background(), `
		SELECT CAST(updated_at AS TEXT)
		FROM agents
		WHERE id = '00000000-0000-4000-8000-000000000105'
	`).Scan(&agentUpdatedAt); err != nil {
		t.Fatal(err)
	}
	var agentID string
	var generation int64
	var credentialHash []byte
	var createdAt, lastAuthenticatedAt string
	var revokedAt sql.NullString
	if err := handle.DB.QueryRowContext(context.Background(), `
		SELECT agent_id, generation, credential_hash,
		       CAST(created_at AS TEXT), CAST(revoked_at AS TEXT), CAST(last_authenticated_at AS TEXT)
		FROM agent_credentials
		WHERE agent_id = '00000000-0000-4000-8000-000000000105'
	`).Scan(&agentID, &generation, &credentialHash, &createdAt, &revokedAt, &lastAuthenticatedAt); err != nil {
		t.Fatal(err)
	}
	if agentID != "00000000-0000-4000-8000-000000000105" ||
		generation != 3 || !bytes.Equal(credentialHash, []byte{1, 2, 3, 4}) ||
		!strings.HasPrefix(agentUpdatedAt, "2026-07-25 12:03:00") && agentUpdatedAt != "2026-07-25T12:03:00Z" ||
		!strings.HasPrefix(createdAt, "2026-07-25 12:02:00") && createdAt != "2026-07-25T12:02:00Z" ||
		revokedAt.Valid ||
		!strings.HasPrefix(lastAuthenticatedAt, "2026-07-25 12:03:00") && lastAuthenticatedAt != "2026-07-25T12:03:00Z" {
		t.Fatalf(
			"upgraded credential agent=%q generation=%d hash=%x agent_updated=%q created=%q revoked=%v authenticated=%q",
			agentID, generation, credentialHash, agentUpdatedAt, createdAt, revokedAt, lastAuthenticatedAt,
		)
	}
	// The application port does not expose credential generations yet. This
	// SQL-level lookup freezes the active hash+generation storage invariant
	// until the port is finalized.
	placeholder := "?"
	if handle.Profile == database.ProfilePostgres {
		placeholder = "$1"
	}
	var authenticatedAgentID string
	var authenticatedGeneration int64
	if err := handle.DB.QueryRowContext(context.Background(), `
		SELECT a.id, c.generation
		FROM agent_credentials c
		JOIN agents a ON a.id = c.agent_id
		WHERE c.credential_hash = `+placeholder+`
		  AND c.revoked_at IS NULL
		  AND a.revoked_at IS NULL
	`, []byte{1, 2, 3, 4}).Scan(&authenticatedAgentID, &authenticatedGeneration); err != nil {
		t.Fatal(err)
	}
	if authenticatedAgentID != agentID || authenticatedGeneration != generation {
		t.Fatalf("active credential lookup agent=%q generation=%d", authenticatedAgentID, authenticatedGeneration)
	}
	var revokedUpdatedAt string
	if err := handle.DB.QueryRowContext(context.Background(), `
		SELECT CAST(updated_at AS TEXT)
		FROM agents
		WHERE id = '00000000-0000-4000-8000-000000000106'
	`).Scan(&revokedUpdatedAt); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(revokedUpdatedAt, "2026-07-25 12:05:00") && revokedUpdatedAt != "2026-07-25T12:05:00Z" {
		t.Fatalf("revoked agent updated_at=%q", revokedUpdatedAt)
	}
}

func assertCurrentAgentWriterPopulatesUpdatedAt(t *testing.T, handle *database.Handle) {
	t.Helper()
	createdAt := time.Date(2026, 7, 25, 13, 0, 0, 0, time.UTC)
	agent, err := domain.NewAgent(domain.NewAgentParams{
		ID: "00000000-0000-4000-8000-000000000107", LocationID: "00000000-0000-4000-8000-000000000101",
		Name: "post-upgrade-agent", Capabilities: []domain.AgentCapability{domain.CapabilityHTTP},
		CredentialGeneration: 1, CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Store.Transact(context.Background(), func(ctx context.Context, repositories application.Repositories) error {
		return repositories.Agents.Create(ctx, application.AgentRecord{Agent: agent, CredentialHash: []byte{9, 10, 11, 12}})
	}); err != nil {
		t.Fatal(err)
	}
	var updatedAt string
	if err := handle.DB.QueryRowContext(context.Background(), `
		SELECT CAST(updated_at AS TEXT)
		FROM agents
		WHERE id = '00000000-0000-4000-8000-000000000107'
	`).Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(updatedAt, "2026-07-25 13:00:00") && updatedAt != createdAt.Format(time.RFC3339Nano) {
		t.Fatalf("writer updated_at=%s, want %s", updatedAt, createdAt)
	}
	assertPresentedCredentialLookup(t, handle, agent.ID)
	heartbeatAt := createdAt.Add(time.Hour)
	if err := handle.Store.Transact(context.Background(), func(ctx context.Context, repositories application.Repositories) error {
		updated, err := repositories.Agents.UpdateHeartbeat(
			ctx, agent.ID, agent.CredentialGeneration, "v7", agent.Capabilities, heartbeatAt,
		)
		if err != nil {
			return err
		}
		if !updated {
			return errors.New("heartbeat did not update agent")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := handle.DB.QueryRowContext(context.Background(), `
		SELECT CAST(updated_at AS TEXT)
		FROM agents
		WHERE id = '00000000-0000-4000-8000-000000000107'
	`).Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(updatedAt, "2026-07-25 14:00:00") && updatedAt != heartbeatAt.Format(time.RFC3339Nano) {
		t.Fatalf("heartbeat updated_at=%s, want %s", updatedAt, heartbeatAt)
	}
}

func assertPresentedCredentialLookup(t *testing.T, handle *database.Handle, agentID domain.AgentID) {
	t.Helper()
	ctx := context.Background()
	var credentialCount int
	if err := handle.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM agent_credentials WHERE agent_id = "+profilePlaceholder(handle.Profile, 1),
		string(agentID),
	).Scan(&credentialCount); err != nil {
		t.Fatal(err)
	}
	if credentialCount != 1 {
		t.Fatalf("enrollment credential rows=%d, want 1", credentialCount)
	}
	if err := handle.Store.View(ctx, func(ctx context.Context, repositories application.Repositories) error {
		record, err := repositories.Agents.FindActiveByCredentialHash(ctx, []byte{9, 10, 11, 12})
		if err != nil {
			return err
		}
		if record.Agent.ID != agentID || record.PresentedCredentialGeneration != 1 {
			return fmt.Errorf("generation-1 lookup = %#v", record)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	insert := `INSERT INTO agent_credentials
		(agent_id, generation, credential_hash, created_at)
		VALUES (` + profilePlaceholder(handle.Profile, 1) + `, 2, ` + profilePlaceholder(handle.Profile, 2) + `, ` + profilePlaceholder(handle.Profile, 3) + `)`
	if _, err := handle.DB.ExecContext(ctx, insert, string(agentID), []byte{13, 14, 15, 16}, "2026-07-25T13:30:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.DB.ExecContext(ctx,
		"UPDATE agents SET credential_generation = 2 WHERE id = "+profilePlaceholder(handle.Profile, 1),
		string(agentID),
	); err != nil {
		t.Fatal(err)
	}
	rollbackAgent, err := domain.NewAgent(domain.NewAgentParams{
		ID: "00000000-0000-4000-8000-000000000110", LocationID: "00000000-0000-4000-8000-000000000101",
		Name: "rollback-agent", Capabilities: []domain.AgentCapability{domain.CapabilityHTTP},
		CredentialGeneration: 1, CreatedAt: time.Date(2026, 7, 25, 13, 31, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = handle.Store.Transact(ctx, func(ctx context.Context, repositories application.Repositories) error {
		return repositories.Agents.Create(ctx, application.AgentRecord{
			Agent: rollbackAgent, CredentialHash: []byte{13, 14, 15, 16},
		})
	})
	if err == nil {
		t.Fatal("dual-write accepted a duplicate generation credential")
	}
	var rollbackAgentCount int
	if scanErr := handle.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM agents WHERE id = "+profilePlaceholder(handle.Profile, 1),
		string(rollbackAgent.ID),
	).Scan(&rollbackAgentCount); scanErr != nil {
		t.Fatal(scanErr)
	}
	if rollbackAgentCount != 0 {
		t.Fatalf("failed credential insert left %d legacy agent rows", rollbackAgentCount)
	}
	if err := handle.Store.View(ctx, func(ctx context.Context, repositories application.Repositories) error {
		record, err := repositories.Agents.FindActiveByCredentialHash(ctx, []byte{13, 14, 15, 16})
		if err != nil {
			return err
		}
		if record.Agent.ID != agentID || record.PresentedCredentialGeneration != 2 || record.Agent.CredentialGeneration != 2 ||
			!bytes.Equal(record.CredentialHash, []byte{13, 14, 15, 16}) {
			return fmt.Errorf("overlap lookup = %#v", record)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	newService := func(hash []byte, observedAt time.Time) *application.AgentService {
		return application.NewAgentService(application.AgentServiceConfig{
			Store: handle.Store, Tokens: fixedCredentialHasher{hash: hash}, Now: func() time.Time { return observedAt },
		})
	}
	oldService := newService([]byte{9, 10, 11, 12}, time.Date(2026, 7, 25, 13, 32, 0, 0, time.UTC))
	oldPrincipal, err := oldService.Authenticate(ctx, "generation-one")
	if err != nil {
		t.Fatal(err)
	}
	if oldPrincipal.CredentialGeneration != 1 {
		t.Fatalf("generation-1 principal=%#v", oldPrincipal)
	}
	if err := oldService.Heartbeat(ctx, oldPrincipal, 1, "overlap-old", []domain.AgentCapability{domain.CapabilityHTTP}); err != nil {
		t.Fatalf("old overlapping heartbeat: %v", err)
	}
	service := newService([]byte{13, 14, 15, 16}, time.Date(2026, 7, 25, 13, 33, 0, 0, time.UTC))
	principal, err := service.Authenticate(ctx, "generation-two")
	if err != nil {
		t.Fatal(err)
	}
	if principal.SubjectID != string(agentID) || principal.CredentialGeneration != 2 {
		t.Fatalf("generation-2 principal=%#v", principal)
	}
	if err := service.Heartbeat(ctx, principal, 2, "overlap-new", []domain.AgentCapability{domain.CapabilityHTTP}); err != nil {
		t.Fatalf("new overlapping heartbeat: %v", err)
	}
	for generation, want := range map[int]string{1: "2026-07-25T13:32:00", 2: "2026-07-25T13:33:00"} {
		var observed string
		query := `SELECT CAST(last_authenticated_at AS TEXT) FROM agent_credentials
			WHERE agent_id = ` + profilePlaceholder(handle.Profile, 1) + ` AND generation = ` + profilePlaceholder(handle.Profile, 2)
		if err := handle.DB.QueryRowContext(ctx, query, string(agentID), generation).Scan(&observed); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Replace(observed, " ", "T", 1), want) {
			t.Fatalf("generation %d last_authenticated_at=%q, want %s", generation, observed, want)
		}
	}
	update := `UPDATE agent_credentials SET revoked_at = ` + profilePlaceholder(handle.Profile, 1) +
		` WHERE agent_id = ` + profilePlaceholder(handle.Profile, 2) + ` AND generation = 2`
	if _, err := handle.DB.ExecContext(ctx, update, "2026-07-25T13:40:00Z", string(agentID)); err != nil {
		t.Fatal(err)
	}
	if err := handle.Store.View(ctx, func(ctx context.Context, repositories application.Repositories) error {
		_, err := repositories.Agents.FindActiveByCredentialHash(ctx, []byte{13, 14, 15, 16})
		return err
	}); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("revoked credential lookup error=%v, want ErrNotFound", err)
	}
	if _, err := service.Authenticate(ctx, "generation-two"); !errors.Is(err, application.ErrInvalidCredentials) {
		t.Fatalf("revoked credential authentication error=%v, want ErrInvalidCredentials", err)
	}
}

type fixedCredentialHasher struct{ hash []byte }

func (fixedCredentialHasher) New() (application.IssuedToken, error) {
	return application.IssuedToken{}, errors.New("not supported")
}

func (h fixedCredentialHasher) Hash(string) []byte {
	return append([]byte(nil), h.hash...)
}

func profilePlaceholder(profile database.Profile, position int) string {
	if profile == database.ProfilePostgres {
		return fmt.Sprintf("$%d", position)
	}
	return "?"
}

func assertAgentUpdatedAtRejectsNull(t *testing.T, handle *database.Handle, postgres bool) {
	t.Helper()
	for _, test := range []struct {
		name      string
		statement string
	}{
		{
			name: "explicit null insert",
			statement: `INSERT INTO agents (
				id, location_id, name, credential_hash, credential_generation,
				capabilities_json, created_at, updated_at
			) VALUES (
				'00000000-0000-4000-8000-000000000108',
				'00000000-0000-4000-8000-000000000101', 'null-agent', ` + credentialHashLiteral(postgres, "0d") + `,
				1, '["http"]', '2026-07-25T13:00:00Z', NULL
			)`,
		},
		{
			name: "omitted insert",
			statement: `INSERT INTO agents (
				id, location_id, name, credential_hash, credential_generation,
				capabilities_json, created_at
			) VALUES (
				'00000000-0000-4000-8000-000000000109',
				'00000000-0000-4000-8000-000000000101', 'omitted-agent', ` + credentialHashLiteral(postgres, "0e") + `,
				1, '["http"]', '2026-07-25T13:00:00Z'
			)`,
		},
		{
			name: "null update",
			statement: `UPDATE agents SET updated_at = NULL
				WHERE id = '00000000-0000-4000-8000-000000000107'`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := handle.DB.ExecContext(context.Background(), test.statement); err == nil {
				t.Fatal("agents.updated_at accepted NULL")
			}
		})
	}
}

func assertPostgresAgentCredentialDowngrade(
	t *testing.T,
	handle *database.Handle,
	provider *goose.Provider,
) {
	t.Helper()
	if _, err := provider.Down(context.Background()); err != nil {
		t.Fatal(err)
	}
	var agentCount int
	if err := handle.DB.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM agents
		WHERE id = '00000000-0000-4000-8000-000000000105'
	`).Scan(&agentCount); err != nil {
		t.Fatal(err)
	}
	if agentCount != 1 {
		t.Fatalf("legacy agent count after downgrade=%d", agentCount)
	}
	if _, err := handle.DB.ExecContext(context.Background(), "SELECT updated_at FROM agents"); err == nil {
		t.Fatal("updated_at remained after PostgreSQL downgrade")
	}
	if _, err := handle.DB.ExecContext(context.Background(), "SELECT 1 FROM agent_credentials"); err == nil {
		t.Fatal("agent_credentials remained after PostgreSQL downgrade")
	}
	for _, index := range []string{
		"locations_name_id", "monitors_display_order_id", "agents_name_id",
		"incidents_opened_id", "incident_events_incident_created_id",
	} {
		var found sql.NullString
		if err := handle.DB.QueryRowContext(context.Background(), "SELECT to_regclass($1)", index).Scan(&found); err != nil {
			t.Fatal(err)
		}
		if found.Valid {
			t.Fatalf("index %s remained after PostgreSQL downgrade", index)
		}
	}
}

func credentialHashLiteral(postgres bool, value string) string {
	if postgres {
		return `decode('` + value + `', 'hex')`
	}
	return `X'` + value + `'`
}

func seedVersionThreeIncident(t *testing.T, handle *database.Handle) {
	t.Helper()
	_, err := handle.DB.ExecContext(context.Background(), `
		INSERT INTO incidents (
			id, monitor_id, state, severity, opened_at, last_transition_at
		) VALUES (
			'00000000-0000-4000-8000-000000000103',
			'00000000-0000-4000-8000-000000000102',
			'down', 'critical', '2026-07-25T12:01:00Z', '2026-07-25T12:01:00Z'
		)
	`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = handle.DB.ExecContext(context.Background(), `
		INSERT INTO incident_events (
			id, incident_id, previous_state, state, severity, created_at
		) VALUES (
			'00000000-0000-4000-8000-000000000104',
			'00000000-0000-4000-8000-000000000103',
			NULL, 'down', 'critical', '2026-07-25T12:01:00Z'
		)
	`)
	if err != nil {
		t.Fatal(err)
	}
}

func seedVersionOneMonitor(t *testing.T, handle *database.Handle, postgres bool) {
	t.Helper()
	locationID := "00000000-0000-4000-8000-000000000101"
	monitorID := "00000000-0000-4000-8000-000000000102"
	if _, err := handle.DB.ExecContext(context.Background(), `
		INSERT INTO locations (id, name, created_at)
		VALUES ('`+locationID+`', 'upgrade-location', '2026-07-25T12:00:00Z')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := handle.DB.ExecContext(context.Background(), `
		INSERT INTO monitors (
			id, name, kind, interval_ms, timeout_ms, failure_threshold,
			recovery_threshold, http_json, enabled, next_run_at, created_at, updated_at
		) VALUES (
			'`+monitorID+`', 'upgrade-monitor', 'http', 60000, 5000, 3, 2,
			'{"Method":"GET","URL":"https://example.com"}',
			`+booleanLiteral(postgres)+`, '2026-07-25T12:00:00Z', '2026-07-25T12:00:00Z', '2026-07-25T12:00:00Z'
		)
	`); err != nil {
		t.Fatal(err)
	}
}

func assertUpgradedMonitor(t *testing.T, handle *database.Handle) {
	t.Helper()
	if err := handle.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	var kind, description string
	var probe, labels []byte
	var displayOrder int32
	var public bool
	if err := handle.DB.QueryRowContext(
		context.Background(),
		`SELECT kind, probe_json, description, labels_json, display_order, public
		 FROM monitors WHERE name = 'upgrade-monitor'`,
	).Scan(&kind, &probe, &description, &labels, &displayOrder, &public); err != nil {
		t.Fatal(err)
	}
	if kind != "http" || len(probe) == 0 || description != "" ||
		string(labels) != "{}" || displayOrder != 0 || public {
		t.Fatalf(
			"upgraded monitor kind=%q probe=%s description=%q labels=%s order=%d public=%v",
			kind, probe, description, labels, displayOrder, public,
		)
	}
}

func assertUpgradedIncident(t *testing.T, handle *database.Handle) {
	t.Helper()
	var action, state string
	var recoveredAt any
	if err := handle.DB.QueryRowContext(
		context.Background(),
		`SELECT e.action, i.state, i.recovered_at
		 FROM incident_events e
		 JOIN incidents i ON i.id = e.incident_id
		 WHERE e.id = '00000000-0000-4000-8000-000000000104'`,
	).Scan(&action, &state, &recoveredAt); err != nil {
		t.Fatal(err)
	}
	if action != "open" || state != "down" || recoveredAt != nil {
		t.Fatalf("upgraded incident action=%q state=%q recovered=%v", action, state, recoveredAt)
	}
}

func booleanLiteral(postgres bool) string {
	if postgres {
		return "TRUE"
	}
	return "1"
}

func randomHex(t *testing.T) string {
	t.Helper()
	value := make([]byte, 6)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(fmt.Errorf("generate schema suffix: %w", err))
	}
	return hex.EncodeToString(value)
}
