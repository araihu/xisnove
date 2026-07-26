-- name: CreateDiscoveryBatch :execrows
INSERT INTO discovery_batches (agent_id, batch_id, request_hash, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (agent_id, batch_id) DO NOTHING;

-- name: GetDiscoveryBatch :one
SELECT * FROM discovery_batches WHERE agent_id = ? AND batch_id = ?;

-- name: CompleteDiscoveryBatch :execrows
UPDATE discovery_batches
SET accepted = ?, created_count = ?, updated_count = ?, completed_at = ?
WHERE agent_id = ? AND batch_id = ? AND completed_at IS NULL;

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
SET namespace = ?, name = ?, labels_json = ?, network_perspective = ?, present = ?,
    last_observed_at = ?, drift_hint = ?, updated_at = ?
WHERE agent_id = ? AND location_id = ? AND source_kind = ? AND source_uid = ?
  AND protocol = ? AND target = ?;

-- name: GetDiscoveryCandidate :one
SELECT * FROM discovery_candidates WHERE id = ?;

-- name: ListDiscoveryCandidates :many
SELECT * FROM discovery_candidates
WHERE (
    sqlc.arg(state) = '' OR
    (sqlc.arg(state) = 'stale' AND present = 0) OR
    (sqlc.arg(state) = 'promoted' AND promoted_monitor_id IS NOT NULL) OR
    (sqlc.arg(state) = 'pending' AND present = 1 AND promoted_monitor_id IS NULL)
) AND (sqlc.arg(after_id) = '' OR id > sqlc.arg(after_id))
ORDER BY id
LIMIT sqlc.arg(row_limit);

-- name: LinkDiscoveryPromotion :execrows
UPDATE discovery_candidates
SET promoted_monitor_id = ?, updated_at = ?
WHERE id = ? AND promoted_monitor_id IS NULL;
