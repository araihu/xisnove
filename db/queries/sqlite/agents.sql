-- name: CreateAgentEnrollmentToken :exec
INSERT INTO agent_enrollment_tokens (
  id, location_id, token_hash, expires_at, consumed_at, created_at
) VALUES (?, ?, ?, ?, NULL, ?);

-- name: ConsumeAgentEnrollmentToken :one
UPDATE agent_enrollment_tokens
SET consumed_at = sqlc.arg(consumed_at)
WHERE token_hash = sqlc.arg(token_hash)
  AND consumed_at IS NULL
  AND expires_at > sqlc.arg(consumed_at)
RETURNING id, location_id, token_hash, expires_at, consumed_at, created_at;

-- name: CreateAgent :exec
INSERT INTO agents (
  id, location_id, name, credential_hash, credential_generation,
  capabilities_json, version, last_seen_at, revoked_at, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: FindActiveAgentByCredentialHash :one
SELECT *
FROM agents
WHERE credential_hash = ?
  AND revoked_at IS NULL;

-- name: UpdateAgentHeartbeat :execrows
UPDATE agents
SET version = sqlc.arg(version),
    credential_generation = sqlc.arg(credential_generation),
    capabilities_json = sqlc.arg(capabilities_json),
    last_seen_at = sqlc.arg(last_seen_at)
WHERE id = sqlc.arg(id)
  AND revoked_at IS NULL
  AND credential_generation = sqlc.arg(credential_generation);
