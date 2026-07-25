-- name: DatabaseNow :one
SELECT CAST(strftime('%Y-%m-%dT%H:%M:%fZ', 'now') AS TEXT) AS database_now;

-- name: InsertScheduledRun :execrows
INSERT INTO check_runs (
  id, monitor_id, location_id, scheduled_for, probe_json, timeout_ms, status
) VALUES (?, ?, ?, ?, ?, ?, 'available')
ON CONFLICT (monitor_id, location_id, scheduled_for) DO NOTHING;

-- name: ClaimHTTPRun :one
UPDATE check_runs
SET status = 'leased',
    lease_agent_id = sqlc.arg(agent_id),
    lease_token_hash = sqlc.arg(lease_token_hash),
    lease_attempt = lease_attempt + 1,
    lease_expires_at = sqlc.arg(lease_expires_at)
WHERE id = (
  SELECT r.id
  FROM check_runs r
  JOIN agents a ON a.id = sqlc.arg(agent_id)
  WHERE r.location_id = a.location_id
    AND r.status IN ('available', 'leased')
    AND (r.status = 'available' OR r.lease_expires_at <= sqlc.arg(now))
    AND r.scheduled_for <= sqlc.arg(now)
    AND a.revoked_at IS NULL
  ORDER BY r.scheduled_for, r.id
  LIMIT 1
)
RETURNING *;

-- name: GetCheckRun :one
SELECT *
FROM check_runs
WHERE id = ?;

-- name: ResolveCheckRun :execrows
UPDATE check_runs
SET status = 'resolved',
    resolved_at = sqlc.arg(resolved_at)
WHERE id = sqlc.arg(id)
  AND status = 'leased'
  AND lease_agent_id = sqlc.arg(agent_id)
  AND lease_token_hash = sqlc.arg(lease_token_hash);
