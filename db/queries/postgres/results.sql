-- name: InsertProbeResult :execrows
INSERT INTO probe_results (
  id, run_id, agent_id, started_at, finished_at, received_at, outcome,
  latency_ms, observed_status, body_assertion_passed, error_code, diagnostic_sample,
  observed_values_json, tls_not_after, protocol_timings_json
) VALUES (
  sqlc.arg(id), sqlc.arg(run_id), sqlc.arg(agent_id), sqlc.arg(started_at),
  sqlc.arg(finished_at), sqlc.arg(received_at), sqlc.arg(outcome),
  sqlc.arg(latency_ms), sqlc.narg(observed_status),
  sqlc.narg(body_assertion_passed), sqlc.narg(error_code),
  sqlc.narg(diagnostic_sample), sqlc.narg(observed_values_json),
  sqlc.narg(tls_not_after), sqlc.narg(protocol_timings_json)
)
ON CONFLICT DO NOTHING;

-- name: GetProbeResultByID :one
SELECT *
FROM probe_results
WHERE id = sqlc.arg(id);

-- name: GetProbeResultByRun :one
SELECT *
FROM probe_results
WHERE run_id = sqlc.arg(run_id);

-- name: ListMonitorHistory :many
SELECT
  pr.id,
  cr.monitor_id,
  cr.location_id,
  pr.received_at,
  pr.outcome,
  pr.latency_ms
FROM probe_results pr
JOIN check_runs cr ON cr.id = pr.run_id
WHERE cr.monitor_id = sqlc.arg(monitor_id)
  AND pr.received_at >= sqlc.arg(starts_at)
  AND pr.received_at < sqlc.arg(ends_at)
ORDER BY pr.received_at DESC, pr.id DESC
LIMIT sqlc.arg(row_limit);
