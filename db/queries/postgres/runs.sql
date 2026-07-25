-- name: DatabaseNow :one
SELECT clock_timestamp()::timestamptz AS database_now;

-- name: InsertScheduledRun :execrows
INSERT INTO check_runs (
  id, monitor_id, location_id, scheduled_for, probe_json, probe_kind, timeout_ms, status
) VALUES (
  sqlc.arg(id), sqlc.arg(monitor_id), sqlc.arg(location_id),
  sqlc.arg(scheduled_for), sqlc.arg(probe_json), sqlc.arg(probe_kind),
  sqlc.arg(timeout_ms), 'available'
)
ON CONFLICT (monitor_id, location_id, scheduled_for) DO NOTHING;

-- name: ClaimProbeRun :one
WITH candidate AS (
  SELECT r.id
  FROM check_runs r
  JOIN agents a ON a.id = sqlc.arg(agent_id)
  WHERE r.location_id = a.location_id
    AND r.status IN ('available', 'leased')
    AND (r.status = 'available' OR r.lease_expires_at <= sqlc.arg(now))
    AND r.scheduled_for <= sqlc.arg(now)
    AND r.probe_kind = ANY(sqlc.arg(capabilities)::text[])
    AND a.revoked_at IS NULL
  ORDER BY r.scheduled_for, r.id
  FOR UPDATE OF r SKIP LOCKED
  LIMIT 1
)
UPDATE check_runs AS r
SET status = 'leased',
    lease_agent_id = sqlc.arg(agent_id),
    lease_token_hash = sqlc.arg(lease_token_hash),
    lease_attempt = r.lease_attempt + 1,
    lease_expires_at = sqlc.arg(lease_expires_at)
FROM candidate
WHERE r.id = candidate.id
RETURNING r.*;

-- name: GetCheckRun :one
SELECT *
FROM check_runs
WHERE id = sqlc.arg(id);

-- name: ResolveCheckRun :execrows
UPDATE check_runs
SET status = 'resolved',
    resolved_at = sqlc.arg(resolved_at)
WHERE id = sqlc.arg(id)
  AND status = 'leased'
  AND lease_agent_id = sqlc.arg(agent_id)
  AND lease_token_hash = sqlc.arg(lease_token_hash);
