-- name: CountAdmins :one
SELECT COUNT(*) FROM admins;

-- name: CreateAdmin :exec
INSERT INTO admins (id, email, password_hash, created_at)
VALUES (?, ?, ?, ?);

-- name: FindAdminByEmail :one
SELECT id, email, password_hash, created_at
FROM admins
WHERE email = ?;

-- name: CreateSession :exec
INSERT INTO sessions (id, admin_id, token_hash, expires_at, revoked_at)
VALUES (?, ?, ?, ?, ?);

-- name: FindActiveSessionByTokenHash :one
SELECT id, admin_id, token_hash, expires_at, revoked_at
FROM sessions
WHERE token_hash = ?
  AND julianday(expires_at) > julianday(sqlc.arg(now))
  AND revoked_at IS NULL;
