-- name: CreateMaintenanceInterval :exec
INSERT INTO maintenance_intervals (
  id, monitor_id, starts_at, ends_at, reason, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetMaintenanceInterval :one
SELECT * FROM maintenance_intervals WHERE id = ?;

-- name: ListMaintenanceIntervals :many
SELECT * FROM maintenance_intervals
ORDER BY starts_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: ListActiveMaintenanceIntervals :many
SELECT * FROM maintenance_intervals
WHERE monitor_id = sqlc.arg(monitor_id)
  AND julianday(starts_at) <= julianday(sqlc.arg(now))
  AND (ends_at IS NULL OR julianday(ends_at) > julianday(sqlc.arg(now)))
ORDER BY julianday(starts_at), id;

-- name: EndMaintenanceInterval :execrows
UPDATE maintenance_intervals
SET ends_at = sqlc.arg(ends_at), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND (ends_at IS NULL OR julianday(ends_at) > julianday(sqlc.arg(ends_at)));

-- name: DeleteFutureMaintenanceInterval :execrows
DELETE FROM maintenance_intervals
WHERE id = sqlc.arg(id) AND julianday(starts_at) > julianday(sqlc.arg(now));

-- name: ClaimEndedMaintenanceInterval :one
UPDATE maintenance_intervals
SET end_claim_owner = sqlc.arg(claim_owner),
    end_claim_token_hash = sqlc.arg(claim_token_hash),
    end_claim_expires_at = sqlc.arg(claim_expires_at), updated_at = sqlc.arg(now)
WHERE id = (
  SELECT id FROM maintenance_intervals
  WHERE ends_at IS NOT NULL
    AND julianday(ends_at) <= julianday(sqlc.arg(now))
    AND ended_notification_sent_at IS NULL
    AND (
      end_claim_expires_at IS NULL
      OR julianday(end_claim_expires_at) <= julianday(sqlc.arg(now))
    )
  ORDER BY julianday(ends_at), id
  LIMIT 1
)
AND ended_notification_sent_at IS NULL
AND (
  end_claim_expires_at IS NULL
  OR julianday(end_claim_expires_at) <= julianday(sqlc.arg(now))
)
RETURNING *;

-- name: MarkEndedMaintenanceProcessed :execrows
UPDATE maintenance_intervals
SET ended_notification_sent_at = sqlc.arg(processed_at),
    end_claim_owner = NULL, end_claim_token_hash = NULL,
    end_claim_expires_at = NULL, updated_at = sqlc.arg(processed_at)
WHERE id = sqlc.arg(id)
  AND ended_notification_sent_at IS NULL
  AND end_claim_token_hash = sqlc.arg(claim_token_hash);

-- name: ReleaseEndedMaintenanceClaim :execrows
UPDATE maintenance_intervals
SET end_claim_owner = NULL, end_claim_token_hash = NULL,
    end_claim_expires_at = NULL, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND ended_notification_sent_at IS NULL
  AND end_claim_token_hash = sqlc.arg(claim_token_hash);
