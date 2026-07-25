-- name: CreateMaintenanceInterval :exec
INSERT INTO maintenance_intervals (
  id, monitor_id, starts_at, ends_at, reason, created_at, updated_at
) VALUES (
  sqlc.arg(id), sqlc.arg(monitor_id), sqlc.arg(starts_at), sqlc.narg(ends_at),
  sqlc.arg(reason), sqlc.arg(created_at), sqlc.arg(updated_at)
);

-- name: GetMaintenanceInterval :one
SELECT * FROM maintenance_intervals WHERE id = sqlc.arg(id);

-- name: ListMaintenanceIntervals :many
SELECT * FROM maintenance_intervals
ORDER BY starts_at DESC, id DESC
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: ListActiveMaintenanceIntervals :many
SELECT * FROM maintenance_intervals
WHERE monitor_id = sqlc.arg(monitor_id)
  AND starts_at <= sqlc.arg(now)
  AND (ends_at IS NULL OR ends_at > sqlc.arg(now))
ORDER BY starts_at, id;

-- name: EndMaintenanceInterval :execrows
UPDATE maintenance_intervals
SET ends_at = sqlc.arg(ends_at), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND (ends_at IS NULL OR ends_at > sqlc.arg(ends_at));

-- name: DeleteFutureMaintenanceInterval :execrows
DELETE FROM maintenance_intervals
WHERE id = sqlc.arg(id) AND starts_at > sqlc.arg(now);

-- name: ClaimEndedMaintenanceInterval :one
WITH candidate AS (
  SELECT id FROM maintenance_intervals
  WHERE ends_at IS NOT NULL AND ends_at <= sqlc.arg(now)
    AND ended_notification_sent_at IS NULL
    AND (end_claim_expires_at IS NULL OR end_claim_expires_at <= sqlc.arg(now))
  ORDER BY ends_at, id
  FOR UPDATE SKIP LOCKED
  LIMIT 1
)
UPDATE maintenance_intervals AS interval
SET end_claim_owner = sqlc.arg(claim_owner),
    end_claim_token_hash = sqlc.arg(claim_token_hash),
    end_claim_expires_at = sqlc.arg(claim_expires_at), updated_at = sqlc.arg(now)
FROM candidate
WHERE interval.id = candidate.id
RETURNING interval.*;

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
