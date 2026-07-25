-- name: ListProbeResultsForDailyAggregation :many
SELECT pr.id, cr.monitor_id, pr.received_at, pr.outcome, pr.latency_ms
FROM probe_results pr
JOIN check_runs cr ON cr.id = pr.run_id
WHERE pr.received_at >= sqlc.arg(starts_at) AND pr.received_at < sqlc.arg(ends_at)
ORDER BY pr.received_at, pr.id
LIMIT sqlc.arg(row_limit);

-- name: ClaimOperationLease :one
INSERT INTO operation_leases (
  lease_key, owner, token_hash, expires_at, cursor_json, updated_at
) VALUES (
  sqlc.arg(lease_key), sqlc.arg(owner), sqlc.arg(token_hash),
  sqlc.arg(expires_at), sqlc.arg(cursor_json), sqlc.arg(updated_at)
)
ON CONFLICT (lease_key) DO UPDATE SET
  owner = excluded.owner,
  token_hash = excluded.token_hash,
  expires_at = excluded.expires_at,
  updated_at = excluded.updated_at
WHERE operation_leases.expires_at <= excluded.updated_at
RETURNING operation_leases.*;

-- name: UpdateOperationLeaseCursor :execrows
UPDATE operation_leases
SET cursor_json = sqlc.arg(cursor_json), expires_at = sqlc.arg(expires_at),
    updated_at = sqlc.arg(updated_at)
WHERE lease_key = sqlc.arg(lease_key) AND token_hash = sqlc.arg(token_hash);

-- name: ReleaseOperationLease :execrows
DELETE FROM operation_leases
WHERE lease_key = sqlc.arg(lease_key) AND token_hash = sqlc.arg(token_hash);

-- name: UpsertDailyUptime :exec
INSERT INTO daily_uptime (
  monitor_id, day, passing_count, failing_count, unknown_count, observed_ms, updated_at
) VALUES (
  sqlc.arg(monitor_id), sqlc.arg(day), sqlc.arg(passing_count),
  sqlc.arg(failing_count), sqlc.arg(unknown_count), sqlc.arg(observed_ms),
  sqlc.arg(updated_at)
)
ON CONFLICT (monitor_id, day) DO UPDATE SET
  passing_count = excluded.passing_count,
  failing_count = excluded.failing_count,
  unknown_count = excluded.unknown_count,
  observed_ms = excluded.observed_ms,
  updated_at = excluded.updated_at;

-- name: ListDailyUptime :many
SELECT * FROM daily_uptime
WHERE monitor_id = sqlc.arg(monitor_id)
  AND day >= sqlc.arg(starts_on) AND day < sqlc.arg(ends_on)
ORDER BY day;

-- name: DeleteExpiredProbeResults :execrows
WITH expired AS (
  SELECT candidate.id FROM probe_results AS candidate
  WHERE candidate.received_at < sqlc.arg(cutoff)
  ORDER BY candidate.received_at, candidate.id
  LIMIT sqlc.arg(row_limit)
)
DELETE FROM probe_results
USING expired
WHERE probe_results.id = expired.id;

-- name: DeleteExpiredDailyUptime :execrows
WITH expired AS (
  SELECT candidate.monitor_id, candidate.day FROM daily_uptime AS candidate
  WHERE candidate.day < sqlc.arg(cutoff_day)
  ORDER BY candidate.day, candidate.monitor_id
  LIMIT sqlc.arg(row_limit)
)
DELETE FROM daily_uptime
USING expired
WHERE daily_uptime.monitor_id = expired.monitor_id
  AND daily_uptime.day = expired.day;
