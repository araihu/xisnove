-- +goose Up
CREATE TABLE discovery_batches (
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    batch_id TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    accepted INTEGER NOT NULL DEFAULT 0,
    created_count INTEGER NOT NULL DEFAULT 0,
    updated_count INTEGER NOT NULL DEFAULT 0,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (agent_id, batch_id)
);

CREATE TABLE discovery_candidates (
    id UUID PRIMARY KEY,
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    location_id UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    source_kind TEXT NOT NULL,
    source_uid TEXT NOT NULL,
    namespace TEXT NOT NULL,
    name TEXT NOT NULL,
    labels_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    protocol TEXT NOT NULL CHECK (protocol IN ('http', 'tcp', 'dns')),
    target TEXT NOT NULL,
    network_perspective TEXT NOT NULL,
    present BOOLEAN NOT NULL,
    last_observed_at TIMESTAMPTZ NOT NULL,
    promoted_monitor_id UUID REFERENCES monitors(id) ON DELETE SET NULL,
    drift_hint TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
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
