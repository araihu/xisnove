-- +goose Up
CREATE TABLE process_version_leases (
    installation_id TEXT NOT NULL,
    process_id TEXT NOT NULL,
    process_version TEXT NOT NULL,
    minimum_schema_version BIGINT NOT NULL CHECK (minimum_schema_version > 0),
    maximum_schema_version BIGINT NOT NULL CHECK (maximum_schema_version >= minimum_schema_version),
    heartbeat_at_ms BIGINT NOT NULL,
    expires_at_ms BIGINT NOT NULL CHECK (expires_at_ms > heartbeat_at_ms),
    PRIMARY KEY (installation_id, process_id)
);
CREATE INDEX process_version_leases_expiry_idx
    ON process_version_leases (installation_id, expires_at_ms);

CREATE TABLE migration_leases (
    installation_id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    heartbeat_at_ms BIGINT NOT NULL,
    expires_at_ms BIGINT NOT NULL CHECK (expires_at_ms > heartbeat_at_ms)
);

-- +goose Down
DROP TABLE migration_leases;
DROP INDEX process_version_leases_expiry_idx;
DROP TABLE process_version_leases;
