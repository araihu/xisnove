-- name: AppendStateTick :execrows
INSERT INTO state_ticks (
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
) VALUES (
  sqlc.arg(id),
  sqlc.arg(monitor_id),
  sqlc.narg(location_id),
  sqlc.arg(lifecycle),
  sqlc.arg(health),
  sqlc.arg(reason_code),
  sqlc.arg(action_id),
  sqlc.narg(user_action_id),
  sqlc.arg(actor_kind),
  sqlc.narg(actor_id),
  sqlc.arg(occurred_at),
  sqlc.narg(observation_id),
  sqlc.narg(causal_tick_id),
  sqlc.narg(causal_dependency_id)
)
ON CONFLICT (id) DO NOTHING;

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
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg(row_limit);
