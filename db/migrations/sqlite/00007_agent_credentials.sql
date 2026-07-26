-- +goose Up
-- SQLite cannot add a non-null column to this referenced table without a
-- rebuild. Existing rows are backfilled below and triggers enforce the same
-- invariant without rebuilding a table referenced by run/result history.
ALTER TABLE agents ADD COLUMN updated_at TEXT;
UPDATE agents
SET updated_at = CASE
    WHEN revoked_at IS NOT NULL
         AND julianday(revoked_at) >= julianday(created_at)
         AND (last_seen_at IS NULL OR julianday(revoked_at) >= julianday(last_seen_at))
        THEN revoked_at
    WHEN last_seen_at IS NOT NULL AND julianday(last_seen_at) >= julianday(created_at)
        THEN last_seen_at
    ELSE created_at
END;

-- +goose StatementBegin
CREATE TRIGGER agents_updated_at_insert_not_null
BEFORE INSERT ON agents
WHEN NEW.updated_at IS NULL
BEGIN
    SELECT RAISE(ABORT, 'agents.updated_at must not be null');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER agents_updated_at_update_not_null
BEFORE UPDATE OF updated_at ON agents
WHEN NEW.updated_at IS NULL
BEGIN
    SELECT RAISE(ABORT, 'agents.updated_at must not be null');
END;
-- +goose StatementEnd

CREATE TABLE agent_credentials (
    agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    generation INTEGER NOT NULL CHECK (generation > 0),
    credential_hash BLOB NOT NULL UNIQUE,
    created_at TEXT NOT NULL,
    revoked_at TEXT,
    last_authenticated_at TEXT,
    PRIMARY KEY (agent_id, generation)
);

INSERT INTO agent_credentials (
    agent_id, generation, credential_hash, created_at, revoked_at, last_authenticated_at
)
SELECT id, credential_generation, credential_hash, created_at, revoked_at, last_seen_at
FROM agents;

CREATE INDEX locations_name_id ON locations(name, id);
CREATE INDEX monitors_display_order_id ON monitors(display_order, id);
CREATE INDEX agents_name_id ON agents(name, id);
CREATE INDEX incidents_opened_id ON incidents(opened_at DESC, id DESC);
CREATE INDEX incident_events_incident_created_id
    ON incident_events(incident_id, created_at, id);

-- +goose Down
DROP TRIGGER agents_updated_at_update_not_null;
DROP TRIGGER agents_updated_at_insert_not_null;
DROP INDEX incident_events_incident_created_id;
DROP INDEX incidents_opened_id;
DROP INDEX agents_name_id;
DROP INDEX monitors_display_order_id;
DROP INDEX locations_name_id;
DROP TABLE agent_credentials;
ALTER TABLE agents DROP COLUMN updated_at;
