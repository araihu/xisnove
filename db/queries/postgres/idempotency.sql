-- name: GetActiveIdempotencyRecord :one
SELECT principal_id, operation_id, idempotency_key, request_hash,
       resource_kind, resource_id, created_at, expires_at
FROM idempotency_records
WHERE principal_id = sqlc.arg(principal_id)
  AND operation_id = sqlc.arg(operation_id)
  AND idempotency_key = sqlc.arg(idempotency_key)
  AND expires_at > sqlc.arg(now);

-- name: PutIdempotencyRecord :execrows
INSERT INTO idempotency_records (
  principal_id, operation_id, idempotency_key, request_hash,
  resource_kind, resource_id, created_at, expires_at
) VALUES (
  sqlc.arg(principal_id), sqlc.arg(operation_id), sqlc.arg(idempotency_key),
  sqlc.arg(request_hash), sqlc.arg(resource_kind), sqlc.arg(resource_id),
  sqlc.arg(created_at), sqlc.arg(expires_at)
)
ON CONFLICT (principal_id, operation_id, idempotency_key) DO UPDATE SET
  request_hash = excluded.request_hash,
  resource_kind = excluded.resource_kind,
  resource_id = excluded.resource_id,
  created_at = excluded.created_at,
  expires_at = excluded.expires_at
WHERE idempotency_records.expires_at <= excluded.created_at;

-- name: DeleteExpiredIdempotencyRecords :execrows
WITH expired AS (
  SELECT candidate.principal_id, candidate.operation_id, candidate.idempotency_key
  FROM idempotency_records AS candidate
  WHERE candidate.expires_at <= sqlc.arg(cutoff)
  ORDER BY candidate.expires_at, candidate.principal_id, candidate.operation_id, candidate.idempotency_key
  LIMIT sqlc.arg(row_limit)
)
DELETE FROM idempotency_records
USING expired
WHERE idempotency_records.principal_id = expired.principal_id
  AND idempotency_records.operation_id = expired.operation_id
  AND idempotency_records.idempotency_key = expired.idempotency_key;
