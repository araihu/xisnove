-- name: ListStateTicks :many
SELECT
  id,
  monitor_id,
  location_id,
  lifecycle,
  health,
  reason_code,
  action_id,
  user_action_id,
  actor_kind,
  actor_id,
  occurred_at,
  observation_id,
  causal_tick_id,
  causal_dependency_id
FROM state_ticks
WHERE monitor_id = sqlc.arg(monitor_id)
  AND occurred_at >= sqlc.arg(starts_at)
  AND occurred_at < sqlc.arg(ends_at)
ORDER BY occurred_at, id
LIMIT sqlc.arg(row_limit);
