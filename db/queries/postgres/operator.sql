-- name: GetOperatorResource :one
SELECT * FROM operator_resources
WHERE owner_key = sqlc.arg(owner_key) AND owner_uid = sqlc.arg(owner_uid) AND kind = sqlc.arg(kind);

-- name: InsertOperatorResource :exec
INSERT INTO operator_resources (owner_key, owner_uid, kind, resource_id, deleted_at)
VALUES (sqlc.arg(owner_key), sqlc.arg(owner_uid), sqlc.arg(kind), sqlc.arg(resource_id), NULL);

-- name: RestoreOperatorResource :execrows
UPDATE operator_resources
SET deleted_at = NULL
WHERE owner_key = sqlc.arg(owner_key) AND owner_uid = sqlc.arg(owner_uid)
  AND kind = sqlc.arg(kind) AND resource_id = sqlc.arg(resource_id);

-- name: TombstoneOperatorResource :execrows
UPDATE operator_resources
SET deleted_at = sqlc.arg(deleted_at)
WHERE owner_key = sqlc.arg(owner_key) AND owner_uid = sqlc.arg(owner_uid) AND kind = sqlc.arg(kind);
