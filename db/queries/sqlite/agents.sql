-- name: CreateAgentEnrollmentToken :exec
INSERT INTO agent_enrollment_tokens (
  id, location_id, token_hash, expires_at, consumed_at, created_at
) VALUES (?, ?, ?, ?, NULL, ?);

-- name: ConsumeAgentEnrollmentToken :one
UPDATE agent_enrollment_tokens
SET consumed_at = sqlc.arg(consumed_at)
WHERE token_hash = sqlc.arg(token_hash)
  AND consumed_at IS NULL
  AND julianday(expires_at) > julianday(sqlc.arg(now))
RETURNING id, location_id, token_hash, expires_at, consumed_at, created_at;

-- name: CreateAgent :exec
INSERT INTO agents (
  id, location_id, name, credential_hash, credential_generation,
  capabilities_json, version, last_seen_at, revoked_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, sqlc.arg(created_at), sqlc.arg(updated_at));

-- name: CreateAgentCredential :exec
INSERT INTO agent_credentials (
  agent_id, generation, credential_hash, created_at, revoked_at, last_authenticated_at
) VALUES (?, ?, ?, ?, ?, ?);

-- name: FindActiveAgentByCredentialHash :one
SELECT a.id, a.location_id, a.name, c.credential_hash,
       a.credential_generation, a.capabilities_json, a.version,
       a.last_seen_at, a.revoked_at, a.created_at, a.updated_at, a.last_complete_discovery_at,
       c.generation AS presented_credential_generation
FROM agent_credentials c
JOIN agents a ON a.id = c.agent_id
WHERE c.credential_hash = ?
  AND c.revoked_at IS NULL
  AND a.revoked_at IS NULL;

-- name: GetAgent :one
SELECT *
FROM agents
WHERE id = ?;

-- name: UpdateAgentHeartbeat :execrows
UPDATE agents
SET version = sqlc.arg(version),
    capabilities_json = sqlc.arg(capabilities_json),
    last_seen_at = sqlc.arg(last_seen_at),
    updated_at = sqlc.arg(last_seen_at)
WHERE id = sqlc.arg(id)
  AND revoked_at IS NULL;

-- name: TouchAgentCredentialAuthentication :execrows
UPDATE agent_credentials
SET last_authenticated_at = sqlc.arg(last_authenticated_at)
WHERE agent_id = sqlc.arg(agent_id)
  AND generation = sqlc.arg(generation)
  AND revoked_at IS NULL;
