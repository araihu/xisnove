-- +goose Up
CREATE TABLE operator_resources (
    owner_key TEXT NOT NULL,
    owner_uid TEXT NOT NULL,
    kind TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    deleted_at TEXT,
    PRIMARY KEY (owner_key, owner_uid, kind),
    UNIQUE (kind, resource_id)
);

ALTER TABLE discovery_batches ADD COLUMN complete INTEGER NOT NULL DEFAULT 0
    CHECK (complete IN (0, 1));
ALTER TABLE discovery_batches ADD COLUMN observed_completed_at TEXT;
ALTER TABLE agents ADD COLUMN last_complete_discovery_at TEXT;

CREATE INDEX discovery_batches_complete_agent_completed_at
    ON discovery_batches (agent_id, complete, completed_at);

-- +goose Down
DROP INDEX discovery_batches_complete_agent_completed_at;
ALTER TABLE agents DROP COLUMN last_complete_discovery_at;
ALTER TABLE discovery_batches DROP COLUMN complete;
ALTER TABLE discovery_batches DROP COLUMN observed_completed_at;
DROP TABLE operator_resources;
