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
