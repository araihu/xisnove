-- +goose Up
ALTER TABLE location_health ADD COLUMN stale_at TEXT;

CREATE INDEX due_location_health
    ON location_health(stale_at, monitor_id, location_id)
    WHERE stale_at IS NOT NULL;

-- +goose Down
DROP INDEX due_location_health;
ALTER TABLE location_health DROP COLUMN stale_at;
