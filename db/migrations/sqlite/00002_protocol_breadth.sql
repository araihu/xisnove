-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = OFF;

CREATE TABLE monitors_v2 (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL CHECK (kind IN ('http', 'tcp', 'dns')),
    interval_ms INTEGER NOT NULL CHECK (interval_ms > 0),
    timeout_ms INTEGER NOT NULL CHECK (timeout_ms > 0 AND timeout_ms < interval_ms),
    failure_threshold INTEGER NOT NULL CHECK (failure_threshold > 0),
    recovery_threshold INTEGER NOT NULL CHECK (recovery_threshold > 0),
    probe_json BLOB NOT NULL,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    next_run_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

INSERT INTO monitors_v2 (
    id, name, kind, interval_ms, timeout_ms, failure_threshold,
    recovery_threshold, probe_json, enabled, next_run_at, created_at, updated_at
)
SELECT
    id, name, kind, interval_ms, timeout_ms, failure_threshold,
    recovery_threshold, http_json, enabled, next_run_at, created_at, updated_at
FROM monitors;

DROP TABLE monitors;
ALTER TABLE monitors_v2 RENAME TO monitors;

ALTER TABLE check_runs
    ADD COLUMN probe_kind TEXT NOT NULL DEFAULT 'http'
    CHECK (probe_kind IN ('http', 'tcp', 'dns'));

ALTER TABLE probe_results ADD COLUMN observed_values_json BLOB;
ALTER TABLE probe_results ADD COLUMN tls_not_after TEXT;
ALTER TABLE probe_results ADD COLUMN protocol_timings_json BLOB;

CREATE INDEX monitor_schedule ON monitors(enabled, next_run_at, id);

PRAGMA foreign_keys = ON;

-- +goose Down
SELECT xisnove_migration_2_is_irreversible();
