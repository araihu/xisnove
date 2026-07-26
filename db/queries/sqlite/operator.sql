-- name: GetOperatorResource :one
SELECT * FROM operator_resources
WHERE owner_key = ? AND owner_uid = ? AND kind = ?;

-- name: InsertOperatorResource :exec
INSERT INTO operator_resources (owner_key, owner_uid, kind, resource_id, deleted_at)
VALUES (?, ?, ?, ?, NULL);

-- name: RestoreOperatorResource :execrows
UPDATE operator_resources
SET deleted_at = NULL
WHERE owner_key = ? AND owner_uid = ? AND kind = ? AND resource_id = ?;

-- name: TombstoneOperatorResource :execrows
UPDATE operator_resources
SET deleted_at = ?
WHERE owner_key = ? AND owner_uid = ? AND kind = ?;
