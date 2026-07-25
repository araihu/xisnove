-- +goose Up
CREATE TABLE admins (
    id UUID PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE sessions (
    id UUID PRIMARY KEY,
    admin_id UUID NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ
);

CREATE TABLE locations (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE monitors (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL CHECK (kind = 'http'),
    interval_ms BIGINT NOT NULL CHECK (interval_ms > 0),
    timeout_ms BIGINT NOT NULL CHECK (timeout_ms > 0 AND timeout_ms < interval_ms),
    failure_threshold INTEGER NOT NULL CHECK (failure_threshold > 0),
    recovery_threshold INTEGER NOT NULL CHECK (recovery_threshold > 0),
    http_json JSONB NOT NULL,
    enabled BOOLEAN NOT NULL,
    next_run_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE monitor_locations (
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    location_id UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    required BOOLEAN NOT NULL,
    PRIMARY KEY (monitor_id, location_id)
);

CREATE TABLE agents (
    id UUID PRIMARY KEY,
    location_id UUID NOT NULL REFERENCES locations(id),
    name TEXT NOT NULL,
    credential_hash BYTEA NOT NULL UNIQUE,
    credential_generation BIGINT NOT NULL CHECK (credential_generation > 0),
    capabilities_json JSONB NOT NULL,
    version TEXT,
    last_seen_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE agent_enrollment_tokens (
    id UUID PRIMARY KEY,
    location_id UUID NOT NULL REFERENCES locations(id),
    token_hash BYTEA NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE check_runs (
    id UUID PRIMARY KEY,
    monitor_id UUID NOT NULL REFERENCES monitors(id),
    location_id UUID NOT NULL REFERENCES locations(id),
    scheduled_for TIMESTAMPTZ NOT NULL,
    probe_json JSONB NOT NULL,
    timeout_ms BIGINT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('available', 'leased', 'resolved')),
    lease_agent_id UUID REFERENCES agents(id),
    lease_token_hash BYTEA,
    lease_attempt INTEGER NOT NULL DEFAULT 0,
    lease_expires_at TIMESTAMPTZ,
    resolved_at TIMESTAMPTZ,
    UNIQUE (monitor_id, location_id, scheduled_for)
);

CREATE TABLE probe_results (
    id UUID PRIMARY KEY,
    run_id UUID NOT NULL UNIQUE REFERENCES check_runs(id),
    agent_id UUID NOT NULL REFERENCES agents(id),
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('passed', 'failed')),
    latency_ms BIGINT NOT NULL,
    observed_status INTEGER,
    body_assertion_passed BOOLEAN,
    error_code TEXT,
    diagnostic_sample TEXT
);

CREATE TABLE location_health (
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    location_id UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    state TEXT NOT NULL,
    consecutive_failures INTEGER NOT NULL,
    consecutive_successes INTEGER NOT NULL,
    last_observed_at TIMESTAMPTZ,
    last_transition_at TIMESTAMPTZ,
    PRIMARY KEY (monitor_id, location_id)
);

CREATE TABLE monitor_health (
    monitor_id UUID PRIMARY KEY REFERENCES monitors(id) ON DELETE CASCADE,
    state TEXT NOT NULL,
    last_transition_at TIMESTAMPTZ
);

CREATE TABLE incidents (
    id UUID PRIMARY KEY,
    monitor_id UUID NOT NULL REFERENCES monitors(id),
    state TEXT NOT NULL,
    severity TEXT NOT NULL,
    opened_at TIMESTAMPTZ NOT NULL,
    last_transition_at TIMESTAMPTZ NOT NULL,
    recovered_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX one_active_incident_per_monitor
    ON incidents(monitor_id) WHERE recovered_at IS NULL;

CREATE TABLE incident_events (
    id UUID PRIMARY KEY,
    incident_id UUID NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    previous_state TEXT,
    state TEXT NOT NULL,
    severity TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
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
