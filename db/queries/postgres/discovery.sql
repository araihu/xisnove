-- name: CreateDiscoveryBatch :execrows
INSERT INTO discovery_batches (agent_id, batch_id, request_hash, created_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (agent_id, batch_id) DO NOTHING;

-- name: GetDiscoveryBatch :one
SELECT * FROM discovery_batches WHERE agent_id = $1 AND batch_id = $2;

-- name: CompleteDiscoveryBatch :execrows
UPDATE discovery_batches
SET accepted = $1, created_count = $2, updated_count = $3, completed_at = $4
WHERE agent_id = $5 AND batch_id = $6 AND completed_at IS NULL;

-- name: InsertDiscoveryCandidate :execrows
INSERT INTO discovery_candidates (
    id, agent_id, location_id, source_kind, source_uid, namespace, name,
    labels_json, protocol, target, network_perspective, present,
    last_observed_at, promoted_monitor_id, drift_hint, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
ON CONFLICT (agent_id, location_id, source_kind, source_uid, protocol, target) DO NOTHING;

-- name: GetDiscoveryCandidateByIdentity :one
SELECT * FROM discovery_candidates
WHERE agent_id = $1 AND location_id = $2 AND source_kind = $3 AND source_uid = $4
  AND protocol = $5 AND target = $6;

-- name: UpdateDiscoveryCandidateByIdentity :execrows
UPDATE discovery_candidates
SET drift_hint = CASE
        WHEN promoted_monitor_id IS NOT NULL AND (
            name <> sqlc.arg(name)::text OR namespace <> sqlc.arg(namespace)::text OR
            labels_json <> sqlc.arg(labels_json)::jsonb OR
            network_perspective <> sqlc.arg(network_perspective)::text
        ) THEN 'source metadata changed after promotion'
        ELSE drift_hint
    END,
    namespace = sqlc.arg(namespace), name = sqlc.arg(name),
    labels_json = sqlc.arg(labels_json), network_perspective = sqlc.arg(network_perspective),
    present = sqlc.arg(present), last_observed_at = sqlc.arg(last_observed_at),
    updated_at = sqlc.arg(updated_at)
WHERE agent_id = sqlc.arg(agent_id) AND location_id = sqlc.arg(location_id)
  AND source_kind = sqlc.arg(source_kind) AND source_uid = sqlc.arg(source_uid)
  AND protocol = sqlc.arg(protocol) AND target = sqlc.arg(target)
  AND last_observed_at <= sqlc.arg(last_observed_at);

-- name: GetDiscoveryCandidate :one
SELECT * FROM discovery_candidates WHERE id = $1;

-- name: GetDiscoveryCandidateForUpdate :one
SELECT * FROM discovery_candidates WHERE id = $1 FOR UPDATE;

-- name: ListDiscoveryCandidates :many
SELECT * FROM discovery_candidates
WHERE (
    sqlc.arg(state)::text = '' OR
    (sqlc.arg(state)::text = 'promoted' AND promoted_monitor_id IS NOT NULL) OR
    (sqlc.arg(state)::text = 'pending' AND promoted_monitor_id IS NULL)
) AND (sqlc.narg(present_filter)::boolean IS NULL OR present = sqlc.narg(present_filter)::boolean)
  AND (sqlc.narg(after_id)::uuid IS NULL OR id > sqlc.narg(after_id)::uuid)
ORDER BY id
LIMIT sqlc.arg(row_limit);

-- name: LinkDiscoveryPromotion :execrows
UPDATE discovery_candidates
SET promoted_monitor_id = $1, updated_at = $2
WHERE id = $3 AND promoted_monitor_id IS NULL;
