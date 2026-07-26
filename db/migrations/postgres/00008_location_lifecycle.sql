-- +goose Up
ALTER TABLE locations ADD COLUMN enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE locations ADD COLUMN updated_at TIMESTAMPTZ;
UPDATE locations SET updated_at = created_at;
ALTER TABLE locations ALTER COLUMN updated_at SET NOT NULL;

-- +goose Down
ALTER TABLE locations DROP COLUMN updated_at;
ALTER TABLE locations DROP COLUMN enabled;
