-- +goose Up
CREATE TABLE admins (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    admin_id TEXT NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
    token_hash BLOB NOT NULL UNIQUE,
    expires_at TEXT NOT NULL,
    revoked_at TEXT
);

CREATE TABLE locations (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
);

CREATE TABLE monitors (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL CHECK (kind = 'http'),
    interval_ms INTEGER NOT NULL CHECK (interval_ms > 0),
    timeout_ms INTEGER NOT NULL CHECK (timeout_ms > 0 AND timeout_ms < interval_ms),
    failure_threshold INTEGER NOT NULL CHECK (failure_threshold > 0),
    recovery_threshold INTEGER NOT NULL CHECK (recovery_threshold > 0),
    http_json BLOB NOT NULL,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    next_run_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE monitor_locations (
    monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    location_id TEXT NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    required INTEGER NOT NULL CHECK (required IN (0, 1)),
    PRIMARY KEY (monitor_id, location_id)
);

CREATE TABLE agents (
    id TEXT PRIMARY KEY,
    location_id TEXT NOT NULL REFERENCES locations(id),
    name TEXT NOT NULL,
    credential_hash BLOB NOT NULL UNIQUE,
    credential_generation INTEGER NOT NULL,
    capabilities_json BLOB NOT NULL,
    version TEXT,
    last_seen_at TEXT,
    revoked_at TEXT,
    created_at TEXT NOT NULL
);

CREATE TABLE agent_enrollment_tokens (
    id TEXT PRIMARY KEY,
    location_id TEXT NOT NULL REFERENCES locations(id),
    token_hash BLOB NOT NULL UNIQUE,
    expires_at TEXT NOT NULL,
    consumed_at TEXT,
    created_at TEXT NOT NULL
);

CREATE TABLE check_runs (
    id TEXT PRIMARY KEY,
    monitor_id TEXT NOT NULL REFERENCES monitors(id),
    location_id TEXT NOT NULL REFERENCES locations(id),
    scheduled_for TEXT NOT NULL,
    probe_json BLOB NOT NULL,
    timeout_ms INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('available', 'leased', 'resolved')),
    lease_agent_id TEXT REFERENCES agents(id),
    lease_token_hash BLOB,
    lease_attempt INTEGER NOT NULL DEFAULT 0,
    lease_expires_at TEXT,
    resolved_at TEXT,
    UNIQUE (monitor_id, location_id, scheduled_for)
);

CREATE TABLE probe_results (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL UNIQUE REFERENCES check_runs(id),
    agent_id TEXT NOT NULL REFERENCES agents(id),
    started_at TEXT NOT NULL,
    finished_at TEXT NOT NULL,
    received_at TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('passed', 'failed')),
    latency_ms INTEGER NOT NULL,
    observed_status INTEGER,
    body_assertion_passed INTEGER,
    error_code TEXT,
    diagnostic_sample TEXT
);

CREATE TABLE location_health (
    monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    location_id TEXT NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    state TEXT NOT NULL,
    consecutive_failures INTEGER NOT NULL,
    consecutive_successes INTEGER NOT NULL,
    last_observed_at TEXT,
    last_transition_at TEXT,
    PRIMARY KEY (monitor_id, location_id)
);

CREATE TABLE monitor_health (
    monitor_id TEXT PRIMARY KEY REFERENCES monitors(id) ON DELETE CASCADE,
    state TEXT NOT NULL,
    last_transition_at TEXT
);

CREATE TABLE incidents (
    id TEXT PRIMARY KEY,
    monitor_id TEXT NOT NULL REFERENCES monitors(id),
    state TEXT NOT NULL,
    severity TEXT NOT NULL,
    opened_at TEXT NOT NULL,
    last_transition_at TEXT NOT NULL,
    recovered_at TEXT
);

CREATE UNIQUE INDEX one_active_incident_per_monitor
    ON incidents(monitor_id) WHERE recovered_at IS NULL;

CREATE TABLE incident_events (
    id TEXT PRIMARY KEY,
    incident_id TEXT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    previous_state TEXT,
    state TEXT NOT NULL,
    severity TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX available_runs ON check_runs(status, scheduled_for, lease_expires_at);

-- +goose Down
DROP TABLE incident_events;
DROP TABLE incidents;
DROP TABLE monitor_health;
DROP TABLE location_health;
DROP TABLE probe_results;
DROP TABLE check_runs;
DROP TABLE agent_enrollment_tokens;
DROP TABLE agents;
DROP TABLE monitor_locations;
DROP TABLE monitors;
DROP TABLE locations;
DROP TABLE sessions;
DROP TABLE admins;
