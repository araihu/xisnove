-- name: GetActiveIncidentByMonitor :one
SELECT *
FROM incidents
WHERE monitor_id = sqlc.arg(monitor_id)
  AND recovered_at IS NULL;

-- name: OpenIncident :exec
INSERT INTO incidents (
  id, monitor_id, state, severity, opened_at, last_transition_at, recovered_at
) VALUES (
  sqlc.arg(id), sqlc.arg(monitor_id), sqlc.arg(state), sqlc.arg(severity),
  sqlc.arg(opened_at), sqlc.arg(last_transition_at), NULL
);

-- name: ChangeIncident :execrows
UPDATE incidents
SET state = sqlc.arg(state),
    severity = sqlc.arg(severity),
    last_transition_at = sqlc.arg(last_transition_at)
WHERE id = sqlc.arg(id)
  AND recovered_at IS NULL;

-- name: RecoverIncident :execrows
UPDATE incidents
SET state = sqlc.arg(state),
    last_transition_at = sqlc.arg(last_transition_at),
    recovered_at = sqlc.arg(recovered_at)
WHERE id = sqlc.arg(id)
  AND recovered_at IS NULL;

-- name: InsertIncidentEvent :exec
INSERT INTO incident_events (
  id, incident_id, action, previous_state, state, severity, created_at
) VALUES (
  sqlc.arg(id), sqlc.arg(incident_id), sqlc.arg(action), sqlc.narg(previous_state),
  sqlc.arg(state), sqlc.arg(severity), sqlc.arg(created_at)
);
