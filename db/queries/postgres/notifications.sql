-- name: CreateNotificationChannel :exec
INSERT INTO notification_channels (
  id, name, kind, encrypted_config, key_version, enabled, created_at, updated_at
) VALUES (
  sqlc.arg(id), sqlc.arg(name), sqlc.arg(kind), sqlc.arg(encrypted_config),
  sqlc.arg(key_version), sqlc.arg(enabled), sqlc.arg(created_at), sqlc.arg(updated_at)
);

-- name: GetNotificationChannel :one
SELECT * FROM notification_channels WHERE id = sqlc.arg(id);

-- name: ListNotificationChannels :many
SELECT * FROM notification_channels ORDER BY name, id
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

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
SELECT id, name, kind, encrypted_config, key_version, enabled, created_at, updated_at
FROM notification_channels
WHERE key_version <> sqlc.arg(active_key_version)
ORDER BY id
LIMIT sqlc.arg(row_limit);

-- name: CreateNotificationRoute :exec
INSERT INTO notification_routes (
  id, name, channel_id, monitor_id, label_matchers_json, actions_json,
  severities_json, template, enabled, precedence, created_at, updated_at
) VALUES (
  sqlc.arg(id), sqlc.arg(route_name), sqlc.arg(channel_id), sqlc.narg(monitor_id),
  sqlc.arg(label_matchers_json), sqlc.arg(actions_json), sqlc.arg(severities_json),
  sqlc.arg(template), sqlc.arg(enabled), sqlc.arg(precedence),
  sqlc.arg(created_at), sqlc.arg(updated_at)
);

-- name: GetNotificationRoute :one
SELECT id, name, channel_id, monitor_id, label_matchers_json, actions_json,
       severities_json, template, enabled, precedence, created_at, updated_at
FROM notification_routes WHERE id = sqlc.arg(id);

-- name: ListNotificationRoutes :many
SELECT id, name, channel_id, monitor_id, label_matchers_json, actions_json,
       severities_json, template, enabled, precedence, created_at, updated_at
FROM notification_routes ORDER BY precedence, id
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: ListEnabledNotificationRoutes :many
SELECT id, name, channel_id, monitor_id, label_matchers_json, actions_json,
       severities_json, template, enabled, precedence, created_at, updated_at
FROM notification_routes
WHERE enabled
ORDER BY precedence, id;

-- name: UpdateNotificationRoute :execrows
UPDATE notification_routes
SET name = sqlc.arg(route_name), channel_id = sqlc.arg(channel_id),
    monitor_id = sqlc.narg(monitor_id), label_matchers_json = sqlc.arg(label_matchers_json),
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
) VALUES (
  sqlc.arg(id), sqlc.arg(incident_event_id), sqlc.arg(route_id),
  sqlc.arg(channel_id), sqlc.arg(dedupe_key), sqlc.arg(render_snapshot_json),
  sqlc.arg(state), sqlc.arg(available_at), sqlc.arg(attempt_count),
  sqlc.narg(suppressed_at), sqlc.arg(created_at), sqlc.arg(updated_at)
)
ON CONFLICT DO NOTHING;

-- name: GetNotificationOutbox :one
SELECT * FROM notification_outbox WHERE id = sqlc.arg(id);

-- name: ListNotificationOutbox :many
SELECT * FROM notification_outbox
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: ClaimDueNotificationOutbox :one
WITH candidate AS (
  SELECT id
  FROM notification_outbox
  WHERE (
      state IN ('pending', 'retrying') AND available_at <= sqlc.arg(now)
    ) OR (
      state = 'claimed' AND claim_expires_at IS NOT NULL
      AND claim_expires_at <= sqlc.arg(now)
    )
  ORDER BY available_at, id
  FOR UPDATE SKIP LOCKED
  LIMIT 1
)
UPDATE notification_outbox AS outbox
SET state = 'claimed', claim_owner = sqlc.arg(claim_owner),
    claim_token_hash = sqlc.arg(claim_token_hash),
    claim_expires_at = sqlc.arg(claim_expires_at), updated_at = sqlc.arg(now)
FROM candidate
WHERE outbox.id = candidate.id
RETURNING outbox.*;

-- name: AppendNotificationDeliveryAttempt :exec
INSERT INTO notification_delivery_attempts (
  id, outbox_id, ordinal, started_at, finished_at, outcome,
  error_class, diagnostic, provider_receipt
) VALUES (
  sqlc.arg(id), sqlc.arg(outbox_id), sqlc.arg(ordinal), sqlc.arg(started_at),
  sqlc.arg(finished_at), sqlc.arg(outcome), sqlc.narg(error_class),
  sqlc.narg(diagnostic), sqlc.narg(provider_receipt)
);

-- name: ListNotificationDeliveryAttempts :many
SELECT * FROM notification_delivery_attempts
WHERE outbox_id = sqlc.arg(outbox_id)
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
) VALUES (
  sqlc.arg(id), sqlc.arg(kind), sqlc.arg(subject_kind), sqlc.arg(subject_id),
  sqlc.narg(incident_id), sqlc.arg(payload_json), sqlc.arg(created_at)
);

-- name: ListAuditEventsByIncident :many
SELECT * FROM audit_events
WHERE incident_id = sqlc.arg(incident_id)
ORDER BY created_at, id;
