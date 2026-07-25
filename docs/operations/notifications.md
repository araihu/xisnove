# Notification operations

Notifications are durable database work. An Incident transition, its
IncidentEvent, matching immutable delivery rows, and the audit decision commit
in one transaction. Every eligible server replica may claim deliveries; sends
are at least once, so a provider can receive a duplicate if a process stops
after the provider accepts a request but before Xisnove records success.

Channel configuration is encrypted at rest and is never returned by the API.
Keep the notification keyring outside the database and include it in the
installation's backup and disaster-recovery procedure.

## Create the keyring

The keyring is a private regular JSON file. Each key is exactly 32 random bytes
encoded with standard base64; versions are positive integers and
`activeVersion` must name one of the included keys. Xisnove rejects symlinks,
non-regular files, files accessible by group or other users, invalid JSON, and
files larger than 64 KiB.

Create version 1 on an administrative host with `openssl` and `jq`:

```bash
umask 077
mkdir -p /run/xisnove
KEY="$(openssl rand -base64 32)"
jq -n --arg key "$KEY" \
  '{activeVersion: 1, keys: [{version: 1, key: $key}]}' \
  > /run/xisnove/notification-keyring.json
unset KEY
chmod 600 /run/xisnove/notification-keyring.json
```

Mount that file read-only into every server replica. Pass its path either as
`--notification-master-key-file` or
`XISNOVE_NOTIFICATION_MASTER_KEY_FILE`. A server may start without a keyring
only while no notification channels exist; without one, the delivery worker is
disabled and channel writes are rejected.

```bash
xisnove-server serve \
  --database-profile postgres \
  --database-url "$XISNOVE_DATABASE_URL" \
  --notification-master-key-file /run/xisnove/notification-keyring.json
```

Startup validates that the keyring can open every key version stored by a
channel. It does not send the keyring or decrypted configuration to the
database.

## Rotate encryption keys

Rotation is resumable and re-encrypts bounded batches transactionally. Do not
remove an old key until every stored channel has moved to the new version.

1. Back up the database and the current keyring separately.
2. Add a new 32-byte key at a new version, initially leaving the old version
   active. Distribute that transitional keyring and restart every replica so
   all processes can decrypt both versions.
3. Change `activeVersion` to the new version, distribute the file, and roll all
   replicas again. Old replicas already know the new key, so channel writes
   remain readable during the rollout.
4. Run rotation using the same database profile, URL, authentication inputs,
   and new-active keyring:

```bash
xisnove-server notifications keys rotate \
  --database-profile postgres \
  --database-url "$XISNOVE_DATABASE_URL" \
  --notification-master-key-file /run/xisnove/notification-keyring.json \
  --batch-size 100
```

The command repeats batches until fewer than `--batch-size` channels remain and
logs the total rotated count. Run it again; a zero count confirms no channel
needs the active version. Before deleting the old key, test the new-only
keyring against a non-serving process by running the same rotation command: its
startup validation must succeed and its rotated count must remain zero. Then
roll the new-only keyring to all replicas. Never reuse a version number with
different key material.

## Provider secrets and channel creation

The public API currently accepts Shoutrrr `serviceUrl` and the optional
Alertmanager `bearerToken` only as write-only fields and encrypts the whole
configuration immediately. Reads return channel identity, kind, enabled state,
and timestamps only.

Native file references inside notification channel JSON are **pending**: the
repository has a private-file secret resolver, but the current HTTP API and
server composition do not wire it into channel configuration. Until that is
implemented, read a mode-0600 provider secret locally and let `jq` construct
the request without placing the value in a command argument or source file.

```bash
chmod 600 /run/secrets/alertmanager-token
jq -n \
  --arg endpoint 'https://alerts.example.net' \
  --rawfile token /run/secrets/alertmanager-token \
  '{name:"alertmanager",enabled:true,configuration:{kind:"alertmanager",endpoint:$endpoint,bearerToken:($token|sub("\\r?\\n$";""))}}' |
  curl -fsS \
    -H "Authorization: Bearer $ADMIN_TOKEN" \
    -H 'Content-Type: application/json' \
    --data-binary @- "$XISNOVE_URL/v1/notification-channels" |
  jq
```

Alertmanager configuration accepts an absolute HTTP(S) endpoint without user
information. Xisnove appends `/api/v2/alerts` unless the path already ends with
it. Recovery transitions set `endsAt`; other actions create firing alerts.

For Shoutrrr, put the complete service URL in a private file and substitute
`serviceUrl` in the same way. Only the reviewed HTTP-backed schemes are
accepted: `bark`, `discord`, `generic`, `gotify`, `googlechat`, `hangouts`,
`ifttt`, `join`, `lark`, `matrix`, `mattermost`, `notifiarr`, `ntfy`,
`opsgenie`, `pagerduty`, `pushbullet`, `pushover`, `rocketchat`, `signal`,
`slack`, `teams`, `telegram`, `twilio`, `wecom`, and `zulip`.

Create routes only after the channel exists. Routes may match one Monitor,
exact monitor labels, actions (`open`, `change`, `recover`, or
`maintenance-ended`), and severities (`warning` or `critical`). Lower
`precedence` sorts first; every matching enabled route creates its own delivery.
Templates use Go `text/template` against the immutable render snapshot.

```bash
jq -n --arg channel "$CHANNEL_ID" \
  '{name:"all critical",channelId:$channel,labelMatchers:{},actions:["open","change","recover","maintenance-ended"],severities:["critical"],template:"{{.MonitorName}} is {{.State}}",enabled:true,precedence:10}' |
  curl -fsS \
    -H "Authorization: Bearer $ADMIN_TOKEN" \
    -H 'Content-Type: application/json' \
    --data-binary @- "$XISNOVE_URL/v1/notification-routes" |
  jq
```

Deleting a channel or route disables it; history is retained. Updating a route
does not rewrite already-created delivery snapshots.

## Egress policy

Notification HTTP clients resolve and validate every address, pin the validated
address for the dial, revalidate redirects, disable environment HTTP proxies,
and allow at most ten redirects. Loopback, private, link-local, multicast,
unspecified, and `100.64.0.0/10` addresses are blocked by default.

Allow only the specific private ranges that contain intended receivers. A
configured deny range wins over an allow range, which is useful for excluding
Kubernetes service, metadata, or management networks from a broader homelab
allow rule.

```bash
xisnove-server serve \
  --notification-master-key-file /run/xisnove/notification-keyring.json \
  --notification-egress-allow-cidrs '10.42.7.0/24' \
  --notification-egress-deny-cidrs '10.42.7.1/32,10.96.0.0/12'
```

Prefer a dedicated Alertmanager ingress address and a narrow allow CIDR. DNS
must return only allowed addresses: one denied answer rejects the target. Do
not broadly allow the entire home, VPN, cluster, or cloud network merely to
reach one receiver.

## Worker tuning

Defaults are a 20-row claim batch, four concurrent sends, 45-second claim
lease, one-second poll, 15-second send timeout, eight automatic attempts,
five-second initial backoff, and 15-minute backoff cap. The lease must be longer
than the send timeout.

```text
--notification-batch-size
--notification-concurrency
--notification-lease
--notification-poll
--notification-send-timeout
--notification-max-attempts
--notification-backoff-base
--notification-backoff-cap
```

Increase concurrency only after checking provider rate limits, database pool
capacity, and outbox age. Transient failures retry with exponential backoff and
jitter; a permanent provider/configuration failure, or exhaustion of automatic
attempts, moves the delivery to `permanent-failure`.

## Inspect and replay deliveries

All delivery endpoints require the administrator bearer token. List state in
bounded pages, then inspect one delivery and its bounded attempt history:

```bash
curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$XISNOVE_URL/v1/notification-deliveries?limit=100&offset=0" | jq

curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$XISNOVE_URL/v1/notification-deliveries/$DELIVERY_ID" | jq
```

Useful fields are `state`, `availableAt`, `attemptCount`, `lastErrorClass`,
`lastDiagnostic`, `deliveredAt`, `suppressedAt`, and each attempt's outcome,
error class, diagnostic, and provider receipt. Diagnostics are bounded and
redacted, but should still be treated as operational data.

Replay is an explicit, audited operation and is accepted only for a
`permanent-failure` delivery. Fix the channel, credentials, template, egress,
or provider condition first, then:

```bash
curl -fsS -X POST \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$XISNOVE_URL/v1/notification-deliveries/$DELIVERY_ID/replay"
```

An HTTP `202` queues the existing immutable delivery again. Replay does not
guarantee provider deduplication.
