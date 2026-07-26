-- name: CountAdmins :one
SELECT COUNT(*) FROM admins;

-- name: CreateAdmin :exec
INSERT INTO admins (id, email, password_hash, created_at)
VALUES (sqlc.arg(id), sqlc.arg(email), sqlc.arg(password_hash), sqlc.arg(created_at));

-- name: FindAdminByEmail :one
SELECT id, email, password_hash, created_at
FROM admins
WHERE email = sqlc.arg(email);

-- name: CreateSession :exec
INSERT INTO sessions (id, admin_id, token_hash, expires_at, revoked_at)
VALUES (
  sqlc.arg(id), sqlc.arg(admin_id), sqlc.arg(token_hash),
  sqlc.arg(expires_at), sqlc.narg(revoked_at)
);

-- name: FindActiveSessionByTokenHash :one
SELECT id, admin_id, token_hash, expires_at, revoked_at
FROM sessions
WHERE token_hash = sqlc.arg(token_hash)
  AND expires_at > sqlc.arg(now)
  AND revoked_at IS NULL;

-- name: RevokeSession :execrows
UPDATE sessions
SET revoked_at = sqlc.arg(revoked_at)
WHERE id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: CreateAPIToken :exec
INSERT INTO api_tokens (
  id, admin_id, label, token_hash, scopes_json, created_at,
  expires_at, last_used_at, revoked_at
) VALUES (
  sqlc.arg(id), sqlc.arg(admin_id), sqlc.arg(label), sqlc.arg(token_hash),
  sqlc.arg(scopes_json), sqlc.arg(created_at), sqlc.narg(expires_at),
  sqlc.narg(last_used_at), sqlc.narg(revoked_at)
);

-- name: FindActiveAPITokenByTokenHash :one
SELECT id, admin_id, label, token_hash, scopes_json, created_at,
       expires_at, last_used_at, revoked_at
FROM api_tokens
WHERE token_hash = sqlc.arg(token_hash)
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > sqlc.arg(now));

-- name: ListAPITokens :many
SELECT id, admin_id, label, token_hash, scopes_json, created_at,
       expires_at, last_used_at, revoked_at
FROM api_tokens
ORDER BY created_at, id
LIMIT sqlc.arg(row_limit);

-- name: ListAPITokensAfter :many
SELECT id, admin_id, label, token_hash, scopes_json, created_at,
       expires_at, last_used_at, revoked_at
FROM api_tokens
WHERE created_at > sqlc.arg(cursor_created_at)
   OR (created_at = sqlc.arg(cursor_created_at) AND id > sqlc.arg(cursor_id))
ORDER BY created_at, id
LIMIT sqlc.arg(row_limit);

-- name: RevokeAPIToken :execrows
UPDATE api_tokens
SET revoked_at = sqlc.arg(revoked_at)
WHERE id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: TouchAPITokenLastUsed :execrows
UPDATE api_tokens SET last_used_at = sqlc.arg(last_used_at) WHERE id = sqlc.arg(id);
