-- name: CreateDiscoveryBatch :execrows
INSERT INTO discovery_batches (agent_id, batch_id, request_hash, complete, observed_completed_at, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (agent_id, batch_id) DO NOTHING;

-- name: GetDiscoveryBatch :one
SELECT * FROM discovery_batches WHERE agent_id = ? AND batch_id = ?;

-- name: CompleteDiscoveryBatch :execrows
UPDATE discovery_batches
SET accepted = ?, created_count = ?, updated_count = ?, completed_at = ?
WHERE agent_id = ? AND batch_id = ? AND completed_at IS NULL;

-- name: MarkAbsentDiscoveryCandidates :execrows
UPDATE discovery_candidates
SET present = 0, updated_at = ?
WHERE agent_id = ? AND present = 1 AND last_observed_at < ?;

-- name: RecordAgentLastCompleteDiscovery :execrows
UPDATE agents
SET last_complete_discovery_at = sqlc.arg(completed_at)
WHERE id = sqlc.arg(agent_id) AND (
    last_complete_discovery_at IS NULL OR last_complete_discovery_at < sqlc.arg(completed_at)
);

-- name: GetAgentLastCompleteDiscovery :one
SELECT last_complete_discovery_at
FROM agents
WHERE id = ?;

-- name: InsertDiscoveryCandidate :execrows
INSERT INTO discovery_candidates (
    id, agent_id, location_id, source_kind, source_uid, namespace, name,
    labels_json, protocol, target, network_perspective, present,
    last_observed_at, promoted_monitor_id, drift_hint, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (agent_id, location_id, source_kind, source_uid, protocol, target) DO NOTHING;

-- name: GetDiscoveryCandidateByIdentity :one
SELECT * FROM discovery_candidates
WHERE agent_id = ? AND location_id = ? AND source_kind = ? AND source_uid = ?
  AND protocol = ? AND target = ?;

-- name: UpdateDiscoveryCandidateByIdentity :execrows
UPDATE discovery_candidates
SET drift_hint = CASE
        WHEN promoted_monitor_id IS NOT NULL AND (
            name <> sqlc.arg(name) OR namespace <> sqlc.arg(namespace) OR
            labels_json <> sqlc.arg(labels_json) OR
            network_perspective <> sqlc.arg(network_perspective)
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
SELECT * FROM discovery_candidates WHERE id = ?;

-- name: GetDiscoveryCandidateForUpdate :one
SELECT * FROM discovery_candidates WHERE id = ?;

-- name: ListDiscoveryCandidates :many
SELECT * FROM discovery_candidates
WHERE (
    sqlc.arg(state) = '' OR
    (sqlc.arg(state) = 'promoted' AND promoted_monitor_id IS NOT NULL) OR
    (sqlc.arg(state) = 'pending' AND promoted_monitor_id IS NULL)
) AND (sqlc.narg(present_filter) IS NULL OR present = sqlc.narg(present_filter))
  AND (sqlc.arg(after_id) = '' OR id > sqlc.arg(after_id))
ORDER BY id
LIMIT sqlc.arg(row_limit);

-- name: LinkDiscoveryPromotion :execrows
UPDATE discovery_candidates
SET promoted_monitor_id = ?, updated_at = ?
WHERE id = ? AND promoted_monitor_id IS NULL;
