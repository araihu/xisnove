-- name: CreateAgentEnrollmentToken :exec
INSERT INTO agent_enrollment_tokens (
  id, location_id, token_hash, expires_at, consumed_at, created_at
) VALUES (
  sqlc.arg(id), sqlc.arg(location_id), sqlc.arg(token_hash),
  sqlc.arg(expires_at), NULL, sqlc.arg(created_at)
);

-- name: ConsumeAgentEnrollmentToken :one
UPDATE agent_enrollment_tokens
SET consumed_at = sqlc.arg(consumed_at)
WHERE token_hash = sqlc.arg(token_hash)
  AND consumed_at IS NULL
  AND expires_at > sqlc.arg(now)
RETURNING *;

-- name: CreateAgent :exec
INSERT INTO agents (
  id, location_id, name, credential_hash, credential_generation,
  capabilities_json, version, last_seen_at, revoked_at, created_at, updated_at
) VALUES (
  sqlc.arg(id), sqlc.arg(location_id), sqlc.arg(name), sqlc.arg(credential_hash),
  sqlc.arg(credential_generation), sqlc.arg(capabilities_json), sqlc.narg(version),
  sqlc.narg(last_seen_at), sqlc.narg(revoked_at), sqlc.arg(created_at), sqlc.arg(updated_at)
);

-- name: CreateAgentCredential :exec
INSERT INTO agent_credentials (
  agent_id, generation, credential_hash, created_at, revoked_at, last_authenticated_at
) VALUES (
  sqlc.arg(agent_id), sqlc.arg(generation), sqlc.arg(credential_hash),
  sqlc.arg(created_at), sqlc.narg(revoked_at), sqlc.narg(last_authenticated_at)
);

-- name: FindActiveAgentByCredentialHash :one
SELECT a.id, a.location_id, a.name, c.credential_hash,
       a.credential_generation, a.capabilities_json, a.version,
       a.last_seen_at, a.revoked_at, a.created_at, a.updated_at, a.last_complete_discovery_at,
       c.generation AS presented_credential_generation
FROM agent_credentials c
JOIN agents a ON a.id = c.agent_id
WHERE c.credential_hash = sqlc.arg(credential_hash)
  AND c.revoked_at IS NULL
  AND a.revoked_at IS NULL;

-- name: GetAgent :one
SELECT *
FROM agents
WHERE id = sqlc.arg(id);

-- name: GetPresentedAgentCredentialGeneration :one
SELECT COALESCE((
  SELECT generation
  FROM agent_credentials
  WHERE agent_id = sqlc.arg(agent_id)
    AND revoked_at IS NULL
    AND last_authenticated_at IS NOT NULL
  ORDER BY last_authenticated_at DESC, generation DESC
  LIMIT 1
), 0)::BIGINT AS presented_credential_generation;

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
