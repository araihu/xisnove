-- name: ListProbeResultsForDailyAggregation :many
SELECT pr.id, cr.monitor_id, pr.received_at, pr.outcome, pr.latency_ms
FROM probe_results pr
JOIN check_runs cr ON cr.id = pr.run_id
WHERE julianday(pr.received_at) >= julianday(sqlc.arg(starts_at))
  AND julianday(pr.received_at) < julianday(sqlc.arg(ends_at))
ORDER BY julianday(pr.received_at), pr.id
LIMIT sqlc.arg(row_limit);

-- name: ClaimOperationLease :one
INSERT INTO operation_leases (
  lease_key, owner, token_hash, expires_at, cursor_json, updated_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (lease_key) DO UPDATE SET
  owner = excluded.owner,
  token_hash = excluded.token_hash,
  expires_at = excluded.expires_at,
  updated_at = excluded.updated_at
WHERE julianday(operation_leases.expires_at) <= julianday(excluded.updated_at)
RETURNING *;

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
) VALUES (?, ?, ?, ?, ?, ?, ?)
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
DELETE FROM probe_results
WHERE id IN (
  SELECT id FROM probe_results
  WHERE julianday(received_at) < julianday(sqlc.arg(cutoff))
  ORDER BY julianday(received_at), id
  LIMIT sqlc.arg(row_limit)
);

-- name: DeleteExpiredDailyUptime :execrows
DELETE FROM daily_uptime
WHERE (daily_uptime.monitor_id, daily_uptime.day) IN (
  SELECT expired.monitor_id, expired.day FROM daily_uptime AS expired
  WHERE expired.day < sqlc.arg(cutoff_day)
  ORDER BY expired.day, expired.monitor_id
  LIMIT sqlc.arg(row_limit)
);
