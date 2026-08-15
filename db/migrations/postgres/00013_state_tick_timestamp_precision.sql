-- +goose Up
-- TIMESTAMPTZ stores microseconds in PostgreSQL. Keep it for compatibility
-- with existing readers, while using an integer Unix-nanosecond key for new
-- bounded history reads and writes.
ALTER TABLE state_ticks
    ADD COLUMN occurred_at_unix_nanos BIGINT;

-- +goose StatementBegin
CREATE FUNCTION xisnove_state_ticks_fill_occurred_at_unix_nanos()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.occurred_at_unix_nanos IS NULL THEN
        NEW.occurred_at_unix_nanos :=
            round(EXTRACT(EPOCH FROM NEW.occurred_at) * 1000000)::BIGINT * 1000;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER state_ticks_fill_occurred_at_unix_nanos
BEFORE INSERT OR UPDATE OF occurred_at, occurred_at_unix_nanos ON state_ticks
FOR EACH ROW
EXECUTE FUNCTION xisnove_state_ticks_fill_occurred_at_unix_nanos();

-- Existing TIMESTAMPTZ values have microsecond precision, so the backfill is
-- exact for the legacy representation and leaves the nanosecond field ready
-- for new writers.
UPDATE state_ticks
SET occurred_at_unix_nanos =
    round(EXTRACT(EPOCH FROM occurred_at) * 1000000)::BIGINT * 1000
WHERE occurred_at_unix_nanos IS NULL;

ALTER TABLE state_ticks
    ALTER COLUMN occurred_at_unix_nanos SET NOT NULL;

CREATE INDEX state_ticks_monitor_occurred_at_unix_nanos
    ON state_ticks(monitor_id, occurred_at_unix_nanos, id);
CREATE INDEX state_ticks_occurred_at_unix_nanos
    ON state_ticks(occurred_at_unix_nanos, id);

-- +goose Down
DROP INDEX state_ticks_occurred_at_unix_nanos;
DROP INDEX state_ticks_monitor_occurred_at_unix_nanos;
DROP TRIGGER state_ticks_fill_occurred_at_unix_nanos ON state_ticks;
DROP FUNCTION xisnove_state_ticks_fill_occurred_at_unix_nanos();
ALTER TABLE state_ticks DROP COLUMN occurred_at_unix_nanos;
