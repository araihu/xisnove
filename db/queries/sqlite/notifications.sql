-- name: CreateNotificationChannel :exec
INSERT INTO notification_channels (
  id, name, kind, encrypted_config, key_version, enabled, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetNotificationChannel :one
SELECT * FROM notification_channels WHERE id = ?;

-- name: ListNotificationChannels :many
SELECT * FROM notification_channels ORDER BY name, id LIMIT ? OFFSET ?;

-- name: UpdateNotificationChannel :execrows
UPDATE notification_channels
SET name = sqlc.arg(name), kind = sqlc.arg(kind),
    encrypted_config = sqlc.arg(encrypted_config), key_version = sqlc.arg(key_version),
    enabled = sqlc.arg(enabled), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: SetNotificationChannelEnabled :execrows
UPDATE notification_channels
SET enabled = sqlc.arg(enabled), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: ListNotificationChannelKeyVersions :many
SELECT DISTINCT key_version FROM notification_channels ORDER BY key_version;

-- name: ListNotificationChannelsNeedingKeyVersion :many
SELECT * FROM notification_channels
WHERE key_version <> sqlc.arg(active_key_version)
ORDER BY id
LIMIT sqlc.arg(row_limit);

-- name: CreateNotificationRoute :exec
INSERT INTO notification_routes (
  id, name, channel_id, monitor_id, label_matchers_json, actions_json,
  severities_json, template, enabled, precedence, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetNotificationRoute :one
SELECT * FROM notification_routes WHERE id = ?;

-- name: ListNotificationRoutes :many
SELECT * FROM notification_routes ORDER BY precedence, id LIMIT ? OFFSET ?;

-- name: ListEnabledNotificationRoutes :many
SELECT * FROM notification_routes
WHERE enabled = 1
ORDER BY precedence, id;

-- name: UpdateNotificationRoute :execrows
UPDATE notification_routes
SET name = sqlc.arg(name), channel_id = sqlc.arg(channel_id),
    monitor_id = sqlc.arg(monitor_id), label_matchers_json = sqlc.arg(label_matchers_json),
    actions_json = sqlc.arg(actions_json), severities_json = sqlc.arg(severities_json),
    template = sqlc.arg(template), enabled = sqlc.arg(enabled),
    precedence = sqlc.arg(precedence), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: SetNotificationRouteEnabled :execrows
UPDATE notification_routes
SET enabled = sqlc.arg(enabled), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: CreateNotificationOutbox :execrows
INSERT INTO notification_outbox (
  id, incident_event_id, route_id, channel_id, dedupe_key,
  render_snapshot_json, state, available_at, attempt_count,
  suppressed_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetNotificationOutbox :one
SELECT * FROM notification_outbox WHERE id = ?;

-- name: ListNotificationOutbox :many
SELECT * FROM notification_outbox
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: ClaimDueNotificationOutbox :one
UPDATE notification_outbox
SET state = 'claimed', claim_owner = sqlc.arg(claim_owner),
    claim_token_hash = sqlc.arg(claim_token_hash),
    claim_expires_at = sqlc.arg(claim_expires_at), updated_at = sqlc.arg(now)
WHERE id = (
  SELECT id
  FROM notification_outbox
  WHERE (
      state IN ('pending', 'retrying')
      AND julianday(available_at) <= julianday(sqlc.arg(now))
    ) OR (
      state = 'claimed'
      AND claim_expires_at IS NOT NULL
      AND julianday(claim_expires_at) <= julianday(sqlc.arg(now))
    )
  ORDER BY julianday(available_at), id
  LIMIT 1
)
AND (
    state IN ('pending', 'retrying')
    OR (state = 'claimed' AND julianday(claim_expires_at) <= julianday(sqlc.arg(now)))
)
RETURNING *;

-- name: AppendNotificationDeliveryAttempt :exec
INSERT INTO notification_delivery_attempts (
  id, outbox_id, ordinal, started_at, finished_at, outcome,
  error_class, diagnostic, provider_receipt
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListNotificationDeliveryAttempts :many
SELECT * FROM notification_delivery_attempts
WHERE outbox_id = ?
ORDER BY ordinal;

-- name: MarkNotificationDelivered :execrows
UPDATE notification_outbox
SET state = 'delivered', attempt_count = attempt_count + 1,
    delivered_at = sqlc.arg(delivered_at), last_error_class = NULL,
    last_diagnostic = NULL, claim_owner = NULL, claim_token_hash = NULL,
    claim_expires_at = NULL, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND state = 'claimed'
  AND claim_token_hash = sqlc.arg(claim_token_hash);

-- name: MarkNotificationRetrying :execrows
UPDATE notification_outbox
SET state = 'retrying', attempt_count = attempt_count + 1,
    available_at = sqlc.arg(available_at), last_error_class = sqlc.arg(error_class),
    last_diagnostic = sqlc.arg(diagnostic), claim_owner = NULL,
    claim_token_hash = NULL, claim_expires_at = NULL, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND state = 'claimed'
  AND claim_token_hash = sqlc.arg(claim_token_hash);

-- name: MarkNotificationPermanentFailure :execrows
UPDATE notification_outbox
SET state = 'permanent-failure', attempt_count = attempt_count + 1,
    last_error_class = sqlc.arg(error_class), last_diagnostic = sqlc.arg(diagnostic),
    claim_owner = NULL, claim_token_hash = NULL, claim_expires_at = NULL,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND state = 'claimed'
  AND claim_token_hash = sqlc.arg(claim_token_hash);

-- name: MarkNotificationSuppressed :execrows
UPDATE notification_outbox
SET state = 'suppressed', suppressed_at = sqlc.arg(suppressed_at),
    claim_owner = NULL, claim_token_hash = NULL, claim_expires_at = NULL,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND state = 'claimed'
  AND claim_token_hash = sqlc.arg(claim_token_hash);

-- name: ReleaseNotificationClaim :execrows
UPDATE notification_outbox
SET state = 'retrying', available_at = sqlc.arg(available_at),
    claim_owner = NULL, claim_token_hash = NULL, claim_expires_at = NULL,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND state = 'claimed'
  AND claim_token_hash = sqlc.arg(claim_token_hash);

-- name: ReplayNotificationOutbox :execrows
UPDATE notification_outbox
SET state = 'pending', available_at = sqlc.arg(available_at),
    last_error_class = NULL, last_diagnostic = NULL,
    claim_owner = NULL, claim_token_hash = NULL, claim_expires_at = NULL,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND state = 'permanent-failure';

-- name: CreateAuditEvent :exec
INSERT INTO audit_events (
  id, kind, subject_kind, subject_id, incident_id, payload_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListAuditEventsByIncident :many
SELECT * FROM audit_events
WHERE incident_id = ?
ORDER BY created_at, id;

-- name: ListAuditEventsBySubject :many
SELECT * FROM audit_events
WHERE subject_kind = sqlc.arg(subject_kind)
  AND subject_id = sqlc.arg(subject_id)
ORDER BY created_at, id;
