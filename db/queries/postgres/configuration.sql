-- name: CreateLocation :exec
INSERT INTO locations (id, name, created_at)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(created_at));

-- name: GetLocation :one
SELECT id, name, created_at
FROM locations
WHERE id = sqlc.arg(id);

-- name: CreateMonitor :exec
INSERT INTO monitors (
  id, name, kind, interval_ms, timeout_ms, failure_threshold,
  recovery_threshold, probe_json, enabled, next_run_at, created_at, updated_at
) VALUES (
  sqlc.arg(id), sqlc.arg(name), sqlc.arg(kind), sqlc.arg(interval_ms),
  sqlc.arg(timeout_ms), sqlc.arg(failure_threshold), sqlc.arg(recovery_threshold),
  sqlc.arg(probe_json), sqlc.arg(enabled), sqlc.arg(next_run_at),
  sqlc.arg(created_at), sqlc.arg(updated_at)
);

-- name: GetMonitor :one
SELECT *
FROM monitors
WHERE id = sqlc.arg(id);

-- name: AssignMonitorLocation :exec
INSERT INTO monitor_locations (monitor_id, location_id, required)
VALUES (sqlc.arg(monitor_id), sqlc.arg(location_id), sqlc.arg(required))
ON CONFLICT (monitor_id, location_id)
DO UPDATE SET required = excluded.required;

-- name: GetMonitorLocation :one
SELECT monitor_id, location_id, required
FROM monitor_locations
WHERE monitor_id = sqlc.arg(monitor_id)
ORDER BY location_id
LIMIT 1;

-- name: ListDueMonitorLocations :many
SELECT
  m.id,
  m.name,
  m.kind,
  m.interval_ms,
  m.timeout_ms,
  m.failure_threshold,
  m.recovery_threshold,
  m.probe_json,
  m.enabled,
  m.next_run_at,
  m.created_at,
  m.updated_at,
  ml.location_id,
  ml.required
FROM monitors m
JOIN monitor_locations ml ON ml.monitor_id = m.id
WHERE m.enabled
  AND m.next_run_at <= sqlc.arg(now)
ORDER BY m.next_run_at, m.id, ml.location_id
LIMIT sqlc.arg(row_limit);

-- name: AdvanceMonitorSchedule :execrows
UPDATE monitors
SET next_run_at = sqlc.arg(next_run_at),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND next_run_at < sqlc.arg(next_run_at);
