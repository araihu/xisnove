package integration_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	postgresmigrations "github.com/araihu/xisnove/db/migrations/postgres"
	sqlitemigrations "github.com/araihu/xisnove/db/migrations/sqlite"
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
	assertUpgradedAgentCredential(t, handle)
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
			'2026-07-25T12:03:00Z', '2026-07-25T12:04:00Z', '2026-07-25T12:02:00Z'
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
	var createdAt, revokedAt, lastAuthenticatedAt string
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
		!strings.HasPrefix(agentUpdatedAt, "2026-07-25 12:02:00") && agentUpdatedAt != "2026-07-25T12:02:00Z" ||
		!strings.HasPrefix(createdAt, "2026-07-25 12:02:00") && createdAt != "2026-07-25T12:02:00Z" ||
		!strings.HasPrefix(revokedAt, "2026-07-25 12:04:00") && revokedAt != "2026-07-25T12:04:00Z" ||
		!strings.HasPrefix(lastAuthenticatedAt, "2026-07-25 12:03:00") && lastAuthenticatedAt != "2026-07-25T12:03:00Z" {
		t.Fatalf(
			"upgraded credential agent=%q generation=%d hash=%x agent_updated=%q created=%q revoked=%q authenticated=%q",
			agentID, generation, credentialHash, agentUpdatedAt, createdAt, revokedAt, lastAuthenticatedAt,
		)
	}
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
