-- name: InsertProbeResult :execrows
INSERT INTO probe_results (
  id, run_id, agent_id, started_at, finished_at, received_at, outcome,
  latency_ms, observed_status, body_assertion_passed, error_code, diagnostic_sample,
  observed_values_json, tls_not_after, protocol_timings_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetProbeResultByID :one
SELECT *
FROM probe_results
WHERE id = ?;

-- name: GetProbeResultByRun :one
SELECT *
FROM probe_results
WHERE run_id = ?;

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
  AND julianday(pr.received_at) >= julianday(sqlc.arg(starts_at))
  AND julianday(pr.received_at) < julianday(sqlc.arg(ends_at))
ORDER BY julianday(pr.received_at) DESC, pr.id DESC
LIMIT sqlc.arg(row_limit);
