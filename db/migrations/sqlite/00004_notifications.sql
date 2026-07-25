-- +goose Up
ALTER TABLE monitors ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE monitors ADD COLUMN labels_json BLOB NOT NULL DEFAULT X'7B7D';
ALTER TABLE monitors ADD COLUMN display_order INTEGER NOT NULL DEFAULT 0 CHECK (display_order >= 0);
ALTER TABLE monitors ADD COLUMN public INTEGER NOT NULL DEFAULT 0 CHECK (public IN (0, 1));

ALTER TABLE incident_events ADD COLUMN action TEXT NOT NULL DEFAULT 'change'
    CHECK (action IN ('open', 'change', 'recover', 'maintenance-ended'));
UPDATE incident_events
SET action = CASE
    WHEN previous_state IS NULL THEN 'open'
    WHEN state = 'up' THEN 'recover'
    ELSE 'change'
END;

CREATE TABLE notification_channels (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL CHECK (kind IN ('shoutrrr', 'alertmanager')),
    encrypted_config BLOB NOT NULL,
    key_version INTEGER NOT NULL CHECK (key_version > 0),
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE notification_routes (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    channel_id TEXT NOT NULL REFERENCES notification_channels(id),
    monitor_id TEXT REFERENCES monitors(id) ON DELETE CASCADE,
    label_matchers_json BLOB NOT NULL,
    actions_json BLOB NOT NULL,
    severities_json BLOB NOT NULL,
    template TEXT NOT NULL,
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    precedence INTEGER NOT NULL DEFAULT 0 CHECK (precedence >= 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX notification_routes_matching
    ON notification_routes(enabled, precedence, id);

CREATE TABLE notification_outbox (
    id TEXT PRIMARY KEY,
    incident_event_id TEXT NOT NULL REFERENCES incident_events(id) ON DELETE CASCADE,
    route_id TEXT NOT NULL REFERENCES notification_routes(id),
    channel_id TEXT NOT NULL REFERENCES notification_channels(id),
    dedupe_key TEXT NOT NULL UNIQUE,
    render_snapshot_json BLOB NOT NULL,
    state TEXT NOT NULL CHECK (
        state IN ('pending', 'claimed', 'retrying', 'delivered', 'permanent-failure', 'suppressed')
    ),
    available_at TEXT NOT NULL,
    claim_owner TEXT,
    claim_token_hash BLOB,
    claim_expires_at TEXT,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    last_error_class TEXT,
    last_diagnostic TEXT,
    delivered_at TEXT,
    suppressed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (incident_event_id, route_id, channel_id)
);

CREATE INDEX due_notification_outbox
    ON notification_outbox(state, available_at, claim_expires_at, id)
    WHERE state IN ('pending', 'retrying', 'claimed');

CREATE TABLE notification_delivery_attempts (
    id TEXT PRIMARY KEY,
    outbox_id TEXT NOT NULL REFERENCES notification_outbox(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL CHECK (ordinal > 0),
    started_at TEXT NOT NULL,
    finished_at TEXT NOT NULL,
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
    id TEXT PRIMARY KEY,
    monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    starts_at TEXT NOT NULL,
    ends_at TEXT,
    reason TEXT NOT NULL,
    end_claim_owner TEXT,
    end_claim_token_hash BLOB,
    end_claim_expires_at TEXT,
    ended_notification_sent_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (ends_at IS NULL OR julianday(ends_at) > julianday(starts_at))
);

CREATE INDEX active_maintenance_intervals
    ON maintenance_intervals(monitor_id, starts_at, ends_at);

CREATE INDEX due_maintenance_end
    ON maintenance_intervals(ends_at, end_claim_expires_at, id)
    WHERE ends_at IS NOT NULL AND ended_notification_sent_at IS NULL;

CREATE TABLE audit_events (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    subject_kind TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    incident_id TEXT REFERENCES incidents(id) ON DELETE CASCADE,
    payload_json BLOB NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX audit_events_by_subject
    ON audit_events(subject_kind, subject_id, created_at, id);
CREATE INDEX audit_events_by_incident
    ON audit_events(incident_id, created_at, id)
    WHERE incident_id IS NOT NULL;

CREATE TABLE operation_leases (
    lease_key TEXT PRIMARY KEY,
    owner TEXT NOT NULL,
    token_hash BLOB NOT NULL,
    expires_at TEXT NOT NULL,
    cursor_json BLOB NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX operation_leases_by_expiry ON operation_leases(expires_at, lease_key);

CREATE TABLE daily_uptime (
    monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    day TEXT NOT NULL,
    passing_count INTEGER NOT NULL CHECK (passing_count >= 0),
    failing_count INTEGER NOT NULL CHECK (failing_count >= 0),
    unknown_count INTEGER NOT NULL CHECK (unknown_count >= 0),
    observed_ms INTEGER NOT NULL CHECK (observed_ms >= 0),
    updated_at TEXT NOT NULL,
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
