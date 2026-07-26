-- name: ManagementGetLocation :one
SELECT id, name, enabled, created_at, updated_at FROM locations WHERE id = ?;

-- name: ManagementListLocations :many
SELECT id, name, enabled, created_at, updated_at
FROM locations
WHERE sqlc.arg(has_after) = 0
   OR name > sqlc.arg(after_sort)
   OR (name = sqlc.arg(after_sort) AND id > sqlc.arg(after_id))
ORDER BY name ASC, id ASC
LIMIT sqlc.arg(row_limit);

-- name: ManagementReplaceLocation :execrows
UPDATE locations
SET name = sqlc.arg(name), enabled = sqlc.arg(enabled), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: ManagementDisableLocation :execrows
UPDATE locations
SET enabled = 0, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND enabled = 1;

-- name: ManagementGetMonitor :one
SELECT m.*, ml.location_id, ml.required
FROM monitors m
JOIN monitor_locations ml ON ml.monitor_id = m.id
WHERE m.id = ?
ORDER BY ml.location_id ASC
LIMIT 1;

-- name: ManagementListMonitors :many
SELECT m.*, ml.location_id, ml.required
FROM monitors m
JOIN monitor_locations ml ON ml.monitor_id = m.id
WHERE sqlc.arg(has_after) = 0
   OR m.display_order > sqlc.arg(after_sort)
   OR (m.display_order = sqlc.arg(after_sort) AND m.id > sqlc.arg(after_id))
ORDER BY m.display_order ASC, m.id ASC
LIMIT sqlc.arg(row_limit);

-- name: ManagementReplaceMonitor :execrows
UPDATE monitors
SET name = sqlc.arg(name), description = sqlc.arg(description),
    labels_json = sqlc.arg(labels_json), display_order = sqlc.arg(display_order),
    public = sqlc.arg(public), kind = sqlc.arg(kind),
    interval_ms = sqlc.arg(interval_ms), timeout_ms = sqlc.arg(timeout_ms),
    failure_threshold = sqlc.arg(failure_threshold),
    recovery_threshold = sqlc.arg(recovery_threshold), probe_json = sqlc.arg(probe_json),
    enabled = sqlc.arg(enabled), next_run_at = sqlc.arg(next_run_at),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: ManagementDeleteMonitorAssignments :exec
DELETE FROM monitor_locations WHERE monitor_id = ?;

-- name: ManagementDisableMonitor :execrows
UPDATE monitors SET enabled = 0, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND enabled = 1;

-- name: ManagementGetAgent :one
SELECT id, location_id, name, credential_generation, capabilities_json,
       version, last_seen_at, revoked_at, created_at, updated_at
FROM agents WHERE id = ?;

-- name: ManagementListAgents :many
SELECT id, location_id, name, credential_generation, capabilities_json,
       version, last_seen_at, revoked_at, created_at, updated_at
FROM agents
WHERE sqlc.arg(has_after) = 0
   OR name > sqlc.arg(after_sort)
   OR (name = sqlc.arg(after_sort) AND id > sqlc.arg(after_id))
ORDER BY name ASC, id ASC
LIMIT sqlc.arg(row_limit);

-- name: ManagementUpdateAgent :execrows
UPDATE agents
SET location_id = sqlc.arg(location_id), name = sqlc.arg(name),
    capabilities_json = sqlc.arg(capabilities_json), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: ManagementRevokeAgent :execrows
UPDATE agents
SET revoked_at = sqlc.arg(revoked_at), updated_at = sqlc.arg(revoked_at)
WHERE id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: ManagementRevokeAllAgentCredentials :exec
UPDATE agent_credentials
SET revoked_at = sqlc.arg(revoked_at)
WHERE agent_id = sqlc.arg(agent_id) AND revoked_at IS NULL;

-- name: ManagementAdvanceAgentGeneration :execrows
UPDATE agents
SET credential_generation = sqlc.arg(new_generation), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(agent_id)
  AND credential_generation = sqlc.arg(expected_generation)
  AND revoked_at IS NULL
  AND (SELECT COUNT(*) FROM agent_credentials c
       WHERE c.agent_id = agents.id AND c.revoked_at IS NULL) < 2;

-- name: ManagementGetAgentCredential :one
SELECT * FROM agent_credentials WHERE agent_id = ? AND generation = ?;

-- name: ManagementGetCurrentAgentCredential :one
SELECT c.*
FROM agent_credentials c
JOIN agents a ON a.id = c.agent_id AND a.credential_generation = c.generation
WHERE a.id = ?;

-- name: ManagementRevokeAgentCredential :execrows
UPDATE agent_credentials SET revoked_at = sqlc.arg(revoked_at)
WHERE agent_id = sqlc.arg(agent_id) AND generation = sqlc.arg(generation)
  AND revoked_at IS NULL;

-- name: ManagementGetIncident :one
SELECT * FROM incidents WHERE id = ?;

-- name: ManagementListIncidents :many
SELECT * FROM incidents
WHERE (sqlc.arg(resolution) = ''
       OR (sqlc.arg(resolution) = 'open' AND recovered_at IS NULL)
       OR (sqlc.arg(resolution) = 'resolved' AND recovered_at IS NOT NULL))
  AND (sqlc.arg(has_after) = 0
       OR julianday(opened_at) < julianday(sqlc.arg(after_sort))
       OR (julianday(opened_at) = julianday(sqlc.arg(after_sort)) AND id < sqlc.arg(after_id)))
ORDER BY julianday(opened_at) DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: ManagementListIncidentEvents :many
SELECT * FROM incident_events
WHERE incident_id = sqlc.arg(incident_id)
  AND (sqlc.arg(has_after) = 0
       OR julianday(created_at) > julianday(sqlc.arg(after_sort))
       OR (julianday(created_at) = julianday(sqlc.arg(after_sort)) AND id > sqlc.arg(after_id)))
ORDER BY julianday(created_at) ASC, id ASC
LIMIT sqlc.arg(row_limit);
