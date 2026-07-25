-- name: GetLocationHealth :one
SELECT *
FROM location_health
WHERE monitor_id = ?
  AND location_id = ?;

-- name: UpsertLocationHealth :exec
INSERT INTO location_health (
  monitor_id, location_id, state, consecutive_failures,
  consecutive_successes, last_observed_at, last_transition_at, stale_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (monitor_id, location_id) DO UPDATE SET
  state = excluded.state,
  consecutive_failures = excluded.consecutive_failures,
  consecutive_successes = excluded.consecutive_successes,
  last_observed_at = excluded.last_observed_at,
  last_transition_at = excluded.last_transition_at,
  stale_at = excluded.stale_at;

-- name: ListRequiredLocationHealth :many
SELECT
  ml.monitor_id,
  ml.location_id,
  COALESCE(lh.state, 'pending') AS state,
  COALESCE(lh.consecutive_failures, 0) AS consecutive_failures,
  COALESCE(lh.consecutive_successes, 0) AS consecutive_successes,
  lh.last_observed_at,
  lh.last_transition_at,
  lh.stale_at
FROM monitor_locations ml
LEFT JOIN location_health lh
  ON lh.monitor_id = ml.monitor_id
  AND lh.location_id = ml.location_id
WHERE ml.monitor_id = ?
  AND ml.required = 1
ORDER BY ml.location_id;

-- name: ListLocationHealth :many
SELECT
  ml.monitor_id,
  ml.location_id,
  COALESCE(lh.state, 'pending') AS state,
  COALESCE(lh.consecutive_failures, 0) AS consecutive_failures,
  COALESCE(lh.consecutive_successes, 0) AS consecutive_successes,
  lh.last_observed_at,
  lh.last_transition_at,
  lh.stale_at
FROM monitor_locations ml
LEFT JOIN location_health lh
  ON lh.monitor_id = ml.monitor_id
  AND lh.location_id = ml.location_id
WHERE ml.monitor_id = ?
ORDER BY ml.location_id;

-- name: GetMonitorHealth :one
SELECT *
FROM monitor_health
WHERE monitor_id = ?;

-- name: UpsertMonitorHealth :exec
INSERT INTO monitor_health (monitor_id, state, last_transition_at)
VALUES (?, ?, ?)
ON CONFLICT (monitor_id) DO UPDATE SET
  state = excluded.state,
  last_transition_at = excluded.last_transition_at;

-- name: ListStaleLocationHealth :many
SELECT *
FROM location_health
WHERE stale_at IS NOT NULL
  AND julianday(stale_at) <= julianday(sqlc.arg(now))
ORDER BY julianday(stale_at), monitor_id, location_id
LIMIT sqlc.arg(row_limit);

-- name: ClaimStaleLocationHealth :execrows
UPDATE location_health
SET state = 'unknown',
    consecutive_failures = 0,
    consecutive_successes = 0,
    last_transition_at = sqlc.arg(transition_at),
    stale_at = NULL
WHERE monitor_id = sqlc.arg(monitor_id)
  AND location_id = sqlc.arg(location_id)
  AND stale_at = sqlc.arg(stale_at)
  AND julianday(stale_at) <= julianday(sqlc.arg(transition_at));
