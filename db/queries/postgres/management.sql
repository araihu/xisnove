-- name: ManagementGetLocation :one
SELECT id, name, enabled, created_at, updated_at FROM locations WHERE id = sqlc.arg(id);

-- name: ManagementSearchResources :many
SELECT id, name, description, kind,
  CASE
    WHEN lower(name) = lower(sqlc.arg(search_query)) OR lower(id::text) = lower(sqlc.arg(search_query)) THEN 0
    WHEN left(lower(name), length(sqlc.arg(search_query))) = lower(sqlc.arg(search_query)) THEN 1
    WHEN strpos(lower(name), lower(sqlc.arg(search_query))) > 0 THEN 2
    WHEN strpos(lower(description), lower(sqlc.arg(search_query))) > 0 THEN 3
    ELSE 4
  END AS search_rank
FROM monitors
WHERE strpos(lower(name), lower(sqlc.arg(search_query))) > 0
   OR strpos(lower(description), lower(sqlc.arg(search_query))) > 0
   OR strpos(lower(id::text), lower(sqlc.arg(search_query))) > 0
ORDER BY search_rank ASC,
  display_order ASC,
  id ASC
LIMIT sqlc.arg(row_limit);

-- name: ManagementListLocations :many
SELECT id, name, enabled, created_at, updated_at
FROM locations
WHERE NOT sqlc.arg(has_after)::boolean
   OR name > sqlc.arg(after_sort)
   OR (name = sqlc.arg(after_sort) AND id > NULLIF(sqlc.arg(after_id), '')::uuid)
ORDER BY name ASC, id ASC
LIMIT sqlc.arg(row_limit);

-- name: ManagementReplaceLocation :execrows
UPDATE locations
SET name = sqlc.arg(name), enabled = sqlc.arg(enabled), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: ManagementDisableLocation :execrows
UPDATE locations SET enabled = FALSE, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND enabled = TRUE;

-- name: ManagementGetMonitor :one
SELECT m.*, ml.location_id, ml.required
FROM monitors m
JOIN monitor_locations ml
  ON ml.monitor_id = m.id
 AND ml.location_id = (
   SELECT selected.location_id
   FROM monitor_locations selected
   WHERE selected.monitor_id = m.id
   ORDER BY selected.location_id ASC
   LIMIT 1
 )
WHERE m.id = sqlc.arg(id)
ORDER BY ml.location_id ASC
LIMIT 1;

-- name: ManagementListMonitors :many
SELECT m.*, ml.location_id, ml.required
FROM monitors m
JOIN monitor_locations ml
  ON ml.monitor_id = m.id
 AND ml.location_id = (
   SELECT selected.location_id
   FROM monitor_locations selected
   WHERE selected.monitor_id = m.id
   ORDER BY selected.location_id ASC
   LIMIT 1
 )
WHERE NOT sqlc.arg(has_after)::boolean
   OR m.display_order::bigint > sqlc.arg(after_sort)::bigint
   OR (m.display_order::bigint = sqlc.arg(after_sort)::bigint AND m.id > NULLIF(sqlc.arg(after_id)::text, '')::uuid)
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
DELETE FROM monitor_locations WHERE monitor_id = sqlc.arg(monitor_id);

-- name: ManagementDisableMonitor :execrows
UPDATE monitors SET enabled = FALSE, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND enabled = TRUE;

-- name: ManagementGetAgent :one
SELECT id, location_id, name, credential_generation, capabilities_json,
       version, last_seen_at, revoked_at, created_at, updated_at
FROM agents WHERE id = sqlc.arg(id);

-- name: ManagementListAgents :many
SELECT id, location_id, name, credential_generation, capabilities_json,
       version, last_seen_at, revoked_at, created_at, updated_at
FROM agents
WHERE NOT sqlc.arg(has_after)::boolean
   OR name > sqlc.arg(after_sort)
   OR (name = sqlc.arg(after_sort) AND id > NULLIF(sqlc.arg(after_id), '')::uuid)
ORDER BY name ASC, id ASC
LIMIT sqlc.arg(row_limit);

-- name: ManagementUpdateAgent :execrows
UPDATE agents
SET location_id = sqlc.arg(location_id), name = sqlc.arg(name),
    capabilities_json = sqlc.arg(capabilities_json), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: ManagementRevokeAgent :execrows
UPDATE agents SET revoked_at = sqlc.arg(revoked_at), updated_at = sqlc.arg(revoked_at)
WHERE id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: ManagementRevokeAllAgentCredentials :exec
UPDATE agent_credentials SET revoked_at = sqlc.arg(revoked_at)
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
SELECT * FROM agent_credentials
WHERE agent_id = sqlc.arg(agent_id) AND generation = sqlc.arg(generation)
FOR UPDATE;

-- name: ManagementGetCurrentAgentCredential :one
SELECT c.*
FROM agent_credentials c
JOIN agents a ON a.id = c.agent_id AND a.credential_generation = c.generation
WHERE a.id = sqlc.arg(agent_id)
FOR UPDATE OF a, c;

-- name: ManagementRevokeAgentCredential :execrows
UPDATE agent_credentials SET revoked_at = sqlc.arg(revoked_at)
WHERE agent_id = sqlc.arg(agent_id) AND generation = sqlc.arg(generation)
  AND revoked_at IS NULL;

-- name: ManagementGetIncident :one
SELECT * FROM incidents WHERE id = sqlc.arg(id);

-- name: ManagementListIncidents :many
SELECT * FROM incidents
WHERE (sqlc.arg(resolution)::text = ''
       OR (sqlc.arg(resolution)::text = 'open' AND recovered_at IS NULL)
       OR (sqlc.arg(resolution)::text = 'resolved' AND recovered_at IS NOT NULL))
  AND (NOT sqlc.arg(has_after)::boolean
       OR opened_at < sqlc.arg(after_sort)
       OR (opened_at = sqlc.arg(after_sort) AND id < NULLIF(sqlc.arg(after_id), '')::uuid))
ORDER BY opened_at DESC, id DESC
LIMIT sqlc.arg(row_limit);

-- name: ManagementListIncidentEvents :many
SELECT * FROM incident_events
WHERE incident_id = sqlc.arg(incident_id)
  AND (NOT sqlc.arg(has_after)::boolean
       OR created_at > sqlc.arg(after_sort)
       OR (created_at = sqlc.arg(after_sort) AND id > NULLIF(sqlc.arg(after_id), '')::uuid))
ORDER BY created_at ASC, id ASC
LIMIT sqlc.arg(row_limit);
