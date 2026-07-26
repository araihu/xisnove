-- +goose Up
CREATE TABLE discovery_batches (
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    batch_id TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    accepted INTEGER NOT NULL DEFAULT 0,
    created_count INTEGER NOT NULL DEFAULT 0,
    updated_count INTEGER NOT NULL DEFAULT 0,
    completed_at TEXT,
    created_at TEXT NOT NULL,
    PRIMARY KEY (agent_id, batch_id)
);

CREATE TABLE discovery_candidates (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    location_id TEXT NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    source_kind TEXT NOT NULL,
    source_uid TEXT NOT NULL,
    namespace TEXT NOT NULL,
    name TEXT NOT NULL,
    labels_json BLOB NOT NULL DEFAULT X'7B7D',
    protocol TEXT NOT NULL CHECK (protocol IN ('http', 'tcp', 'dns')),
    target TEXT NOT NULL,
    network_perspective TEXT NOT NULL,
    present INTEGER NOT NULL CHECK (present IN (0, 1)),
    last_observed_at TEXT NOT NULL,
    promoted_monitor_id TEXT REFERENCES monitors(id) ON DELETE SET NULL,
    drift_hint TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (agent_id, location_id, source_kind, source_uid, protocol, target)
);

CREATE INDEX discovery_candidates_present_updated_id
    ON discovery_candidates (present, updated_at, id);
CREATE INDEX discovery_candidates_location_updated_id
    ON discovery_candidates (location_id, updated_at, id);

-- +goose Down
DROP INDEX discovery_candidates_location_updated_id;
DROP INDEX discovery_candidates_present_updated_id;
DROP TABLE discovery_candidates;
DROP TABLE discovery_batches;
