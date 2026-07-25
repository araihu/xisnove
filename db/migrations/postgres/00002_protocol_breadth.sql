-- +goose Up
ALTER TABLE monitors DROP CONSTRAINT monitors_kind_check;
ALTER TABLE monitors
    ADD CONSTRAINT monitors_kind_check CHECK (kind IN ('http', 'tcp', 'dns'));
ALTER TABLE monitors RENAME COLUMN http_json TO probe_json;

ALTER TABLE check_runs
    ADD COLUMN probe_kind TEXT NOT NULL DEFAULT 'http'
    CHECK (probe_kind IN ('http', 'tcp', 'dns'));

ALTER TABLE probe_results ADD COLUMN observed_values_json JSONB;
ALTER TABLE probe_results ADD COLUMN tls_not_after TIMESTAMPTZ;
ALTER TABLE probe_results ADD COLUMN protocol_timings_json JSONB;

CREATE INDEX monitor_schedule ON monitors(enabled, next_run_at, id);

-- +goose Down
DROP INDEX monitor_schedule;
ALTER TABLE probe_results DROP COLUMN protocol_timings_json;
ALTER TABLE probe_results DROP COLUMN tls_not_after;
ALTER TABLE probe_results DROP COLUMN observed_values_json;
ALTER TABLE check_runs DROP COLUMN probe_kind;
ALTER TABLE monitors RENAME COLUMN probe_json TO http_json;
ALTER TABLE monitors DROP CONSTRAINT monitors_kind_check;
ALTER TABLE monitors
    ADD CONSTRAINT monitors_kind_check CHECK (kind = 'http');
