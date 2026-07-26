-- +goose Up
ALTER TABLE locations ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1
    CHECK (enabled IN (0, 1));
ALTER TABLE locations ADD COLUMN updated_at TEXT;
UPDATE locations SET updated_at = created_at;

-- SQLite cannot add a non-null column with a per-row backfill expression.
-- Current writers populate updated_at and these triggers enforce the invariant.
-- +goose StatementBegin
CREATE TRIGGER locations_updated_at_insert_not_null
BEFORE INSERT ON locations
WHEN NEW.updated_at IS NULL
BEGIN
    SELECT RAISE(ABORT, 'locations.updated_at must not be null');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER locations_updated_at_update_not_null
BEFORE UPDATE OF updated_at ON locations
WHEN NEW.updated_at IS NULL
BEGIN
    SELECT RAISE(ABORT, 'locations.updated_at must not be null');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER locations_updated_at_update_not_null;
DROP TRIGGER locations_updated_at_insert_not_null;
ALTER TABLE locations DROP COLUMN updated_at;
ALTER TABLE locations DROP COLUMN enabled;
