-- +goose Up
ALTER TABLE monitors ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE monitors ADD COLUMN labels_json JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE monitors ADD COLUMN display_order INTEGER NOT NULL DEFAULT 0 CHECK (display_order >= 0);
ALTER TABLE monitors ADD COLUMN public BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE incident_events ADD COLUMN action TEXT NOT NULL DEFAULT 'change'
    CHECK (action IN ('open', 'change', 'recover', 'maintenance-ended'));
UPDATE incident_events
SET action = CASE
    WHEN previous_state IS NULL THEN 'open'
    WHEN state = 'up' THEN 'recover'
    ELSE 'change'
END;

CREATE TABLE notification_channels (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL CHECK (kind IN ('shoutrrr', 'alertmanager')),
    encrypted_config BYTEA NOT NULL,
    key_version INTEGER NOT NULL CHECK (key_version > 0),
    enabled BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE notification_routes (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    channel_id UUID NOT NULL REFERENCES notification_channels(id),
    monitor_id UUID REFERENCES monitors(id) ON DELETE CASCADE,
    label_matchers_json JSONB NOT NULL,
    actions_json JSONB NOT NULL,
    severities_json JSONB NOT NULL,
    template TEXT NOT NULL,
    enabled BOOLEAN NOT NULL,
    precedence INTEGER NOT NULL DEFAULT 0 CHECK (precedence >= 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX notification_routes_matching
    ON notification_routes(enabled, precedence, id);

CREATE TABLE notification_outbox (
    id UUID PRIMARY KEY,
    incident_event_id UUID NOT NULL REFERENCES incident_events(id) ON DELETE CASCADE,
    route_id UUID NOT NULL REFERENCES notification_routes(id),
    channel_id UUID NOT NULL REFERENCES notification_channels(id),
    dedupe_key TEXT NOT NULL UNIQUE,
    render_snapshot_json JSONB NOT NULL,
    state TEXT NOT NULL CHECK (
        state IN ('pending', 'claimed', 'retrying', 'delivered', 'permanent-failure', 'suppressed')
    ),
    available_at TIMESTAMPTZ NOT NULL,
    claim_owner TEXT,
    claim_token_hash BYTEA,
    claim_expires_at TIMESTAMPTZ,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    last_error_class TEXT,
    last_diagnostic TEXT,
    delivered_at TIMESTAMPTZ,
    suppressed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (incident_event_id, route_id, channel_id)
);

CREATE INDEX due_notification_outbox
    ON notification_outbox(state, available_at, claim_expires_at, id)
    WHERE state IN ('pending', 'retrying', 'claimed');

CREATE TABLE notification_delivery_attempts (
    id UUID PRIMARY KEY,
    outbox_id UUID NOT NULL REFERENCES notification_outbox(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL CHECK (ordinal > 0),
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    outcome TEXT NOT NULL CHECK (
        outcome IN ('delivered', 'transient-failure', 'permanent-failure', 'abandoned')
    ),
    error_class TEXT,
    diagnostic TEXT,
    provider_receipt TEXT,
    UNIQUE (outbox_id, ordinal)
);

CREATE INDEX notification_attempts_by_outbox
    ON notification_delivery_attempts(outbox_id, ordinal);

CREATE TABLE maintenance_intervals (
    id UUID PRIMARY KEY,
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ,
    reason TEXT NOT NULL,
    end_claim_owner TEXT,
    end_claim_token_hash BYTEA,
    end_claim_expires_at TIMESTAMPTZ,
    ended_notification_sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (ends_at IS NULL OR ends_at > starts_at)
);

CREATE INDEX active_maintenance_intervals
    ON maintenance_intervals(monitor_id, starts_at, ends_at);

CREATE INDEX due_maintenance_end
    ON maintenance_intervals(ends_at, end_claim_expires_at, id)
    WHERE ends_at IS NOT NULL AND ended_notification_sent_at IS NULL;

CREATE TABLE audit_events (
    id UUID PRIMARY KEY,
    kind TEXT NOT NULL,
    subject_kind TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    incident_id UUID REFERENCES incidents(id) ON DELETE CASCADE,
    payload_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX audit_events_by_subject
    ON audit_events(subject_kind, subject_id, created_at, id);
CREATE INDEX audit_events_by_incident
    ON audit_events(incident_id, created_at, id)
    WHERE incident_id IS NOT NULL;

CREATE TABLE operation_leases (
    lease_key TEXT PRIMARY KEY,
    owner TEXT NOT NULL,
    token_hash BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    cursor_json JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX operation_leases_by_expiry ON operation_leases(expires_at, lease_key);

CREATE TABLE daily_uptime (
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    day DATE NOT NULL,
    passing_count BIGINT NOT NULL CHECK (passing_count >= 0),
    failing_count BIGINT NOT NULL CHECK (failing_count >= 0),
    unknown_count BIGINT NOT NULL CHECK (unknown_count >= 0),
    observed_ms BIGINT NOT NULL CHECK (observed_ms >= 0),
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (monitor_id, day)
);

CREATE INDEX daily_uptime_by_day ON daily_uptime(day, monitor_id);
CREATE INDEX probe_results_received_at ON probe_results(received_at, id);

-- +goose Down
DROP INDEX probe_results_received_at;
DROP TABLE daily_uptime;
DROP TABLE operation_leases;
DROP TABLE audit_events;
DROP TABLE maintenance_intervals;
DROP TABLE notification_delivery_attempts;
DROP TABLE notification_outbox;
DROP TABLE notification_routes;
DROP TABLE notification_channels;
ALTER TABLE incident_events DROP COLUMN action;
ALTER TABLE monitors DROP COLUMN public;
ALTER TABLE monitors DROP COLUMN display_order;
ALTER TABLE monitors DROP COLUMN labels_json;
ALTER TABLE monitors DROP COLUMN description;
