-- name: ListPublicStatusMonitors :many
SELECT
  m.id,
  m.name,
  m.description,
  m.display_order,
  COALESCE(mh.state, 'pending') AS health_state,
  mh.last_transition_at AS health_last_transition_at,
  i.id AS incident_id,
  i.state AS incident_state,
  i.severity AS incident_severity,
  i.opened_at AS incident_opened_at,
  i.last_transition_at AS incident_last_transition_at
FROM monitors m
LEFT JOIN monitor_health mh ON mh.monitor_id = m.id
LEFT JOIN incidents i ON i.monitor_id = m.id AND i.recovered_at IS NULL
WHERE m.public = 1
ORDER BY m.display_order ASC, m.id ASC
LIMIT 1000;
