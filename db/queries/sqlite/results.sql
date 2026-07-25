-- name: InsertProbeResult :execrows
INSERT INTO probe_results (
  id, run_id, agent_id, started_at, finished_at, received_at, outcome,
  latency_ms, observed_status, body_assertion_passed, error_code, diagnostic_sample
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetProbeResultByID :one
SELECT *
FROM probe_results
WHERE id = ?;
