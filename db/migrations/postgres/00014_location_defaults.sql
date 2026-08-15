-- +goose Up
ALTER TABLE locations ADD COLUMN address TEXT NOT NULL DEFAULT '';
ALTER TABLE locations ADD COLUMN protocol TEXT NOT NULL DEFAULT 'http'
    CHECK (protocol IN ('http', 'tcp', 'dns'));
ALTER TABLE locations ADD COLUMN default_interval_ms BIGINT NOT NULL DEFAULT 60000
    CHECK (default_interval_ms > 0);
ALTER TABLE locations ADD COLUMN default_timeout_ms BIGINT NOT NULL DEFAULT 5000
    CHECK (default_timeout_ms > 0 AND default_timeout_ms < default_interval_ms);
ALTER TABLE locations ADD COLUMN default_failure_threshold INTEGER NOT NULL DEFAULT 3
    CHECK (default_failure_threshold > 0);
ALTER TABLE locations ADD COLUMN default_recovery_threshold INTEGER NOT NULL DEFAULT 2
    CHECK (default_recovery_threshold > 0);

-- +goose Down
ALTER TABLE locations DROP COLUMN default_recovery_threshold;
ALTER TABLE locations DROP COLUMN default_failure_threshold;
ALTER TABLE locations DROP COLUMN default_timeout_ms;
ALTER TABLE locations DROP COLUMN default_interval_ms;
ALTER TABLE locations DROP COLUMN protocol;
ALTER TABLE locations DROP COLUMN address;
