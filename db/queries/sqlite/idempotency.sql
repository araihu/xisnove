-- name: GetActiveIdempotencyRecord :one
SELECT principal_id, operation_id, idempotency_key, request_hash,
       resource_kind, resource_id, created_at, expires_at
FROM idempotency_records
WHERE principal_id = sqlc.arg(principal_id)
  AND operation_id = sqlc.arg(operation_id)
  AND idempotency_key = sqlc.arg(idempotency_key)
  AND julianday(expires_at) > julianday(sqlc.arg(now));

-- name: PutIdempotencyRecord :execrows
INSERT INTO idempotency_records (
  principal_id, operation_id, idempotency_key, request_hash,
  resource_kind, resource_id, created_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (principal_id, operation_id, idempotency_key) DO UPDATE SET
  request_hash = excluded.request_hash,
  resource_kind = excluded.resource_kind,
  resource_id = excluded.resource_id,
  created_at = excluded.created_at,
  expires_at = excluded.expires_at
WHERE julianday(idempotency_records.expires_at) <= julianday(excluded.created_at);

-- name: DeleteExpiredIdempotencyRecords :execrows
DELETE FROM idempotency_records
WHERE (principal_id, operation_id, idempotency_key) IN (
  SELECT principal_id, operation_id, idempotency_key
  FROM idempotency_records
  WHERE julianday(expires_at) <= julianday(sqlc.arg(cutoff))
  ORDER BY julianday(expires_at), principal_id, operation_id, idempotency_key
  LIMIT sqlc.arg(row_limit)
);
