# Maintenance and retention operations

Maintenance suppresses notification deliveries, not monitoring. Agents keep
probing, health and Incidents keep changing, IncidentEvents and audits remain
durable, and each matching outbox row is recorded as `suppressed`. This keeps a
complete operational history without sending during the interval.

Retention aggregates raw results into UTC daily uptime and deletes only bounded
batches of expired raw results and daily aggregates. Incident, IncidentEvent,
notification attempt, and audit history is preserved by default.

## Schedule and end maintenance

Maintenance is a one-off interval for one Monitor. `startsAt` is required;
omit `endsAt` for indefinite maintenance. Recurrence is not supported. The end
must be after the start, the reason is required, and elapsed history cannot be
rewritten.

```bash
jq -n \
  --arg monitor "$MONITOR_ID" \
  --arg starts '2026-07-26T02:00:00Z' \
  --arg ends '2026-07-26T03:00:00Z' \
  '{monitorId:$monitor,startsAt:$starts,endsAt:$ends,reason:"router firmware upgrade"}' |
  curl -fsS \
    -H "Authorization: Bearer $ADMIN_TOKEN" \
    -H 'Content-Type: application/json' \
    --data-binary @- "$XISNOVE_URL/v1/maintenance" |
  jq
```

List or inspect intervals with:

```bash
curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$XISNOVE_URL/v1/maintenance?limit=100&offset=0" | jq
curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$XISNOVE_URL/v1/maintenance/$MAINTENANCE_ID" | jq
```

Delete only a future interval that has not started:

```bash
curl -fsS -X DELETE -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$XISNOVE_URL/v1/maintenance/$MAINTENANCE_ID"
```

End an active or indefinite interval at database time:

```bash
curl -fsS -X POST -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$XISNOVE_URL/v1/maintenance/$MAINTENANCE_ID/end" | jq
```

Ending is idempotent. A durable worker later examines ended intervals. If the
Monitor is still `down`, `degraded`, or `unknown`, it appends exactly one
`maintenance-ended` IncidentEvent and applies normal route matching. If the
Monitor has recovered, it emits nothing. The worker defaults to a 100-row
batch, 45-second lease, and one-second poll; these values are currently not
exposed as server flags.

Before maintenance, verify the intended Monitor ID and UTC timestamps, and
ensure routes include `maintenance-ended` if an unhealthy reminder is wanted.
After maintenance, inspect Monitor health, the active Incident, and notification
deliveries. Previously suppressed rows stay suppressed; the synthetic
post-maintenance event creates new deliveries.

## Retention behavior and tuning

The server always runs the retention worker. Defaults are:

| Setting | Default | Accepted bound |
| --- | ---: | ---: |
| `--retention-batch-size` | 500 rows | 1 to 10,000 |
| `--retention-lease` | 45s | greater than 0 |
| `--retention-poll` | 1m | greater than 0 |
| `--retention-probe-results` | 720h (30 days) | at least 24h |
| `--retention-daily-months` | 13 calendar months | 1 to 120 |

Daily aggregation is restart-safe and processes immutable results in UTC day
buckets. Cleanup uses database time and retains rows exactly on the cutoff;
only rows older than it are deleted. Each cycle performs one aggregation page
and at most one bounded cleanup batch per retained history type. Database
leases allow multiple eligible replicas to run without intentionally
duplicating a job.

Example for 90 days of raw results and 24 months of daily history:

```bash
xisnove-server serve \
  --retention-probe-results 2160h \
  --retention-daily-months 24 \
  --retention-batch-size 500 \
  --retention-poll 1m
```

Estimate storage growth before increasing a window. When reducing one, take
and validate a backup first, deploy the new values to every replica, and watch
for `retention cycle failed` log entries. Cleanup is irreversible in the live
database and may require many polls because every transaction is bounded.
Avoid an oversized batch on a latency-sensitive SQLite or local-Turso
installation; a shorter poll interval with a moderate batch usually spreads
work more evenly.

The aggregation cursor begins at the raw-retention horizon. Keep raw history
long enough for late results and successful daily aggregation. Do not reduce
raw retention below the operational delay with which Agents can reconnect and
upload accepted work.

## Backup and restore interaction

Follow the profile-specific [backup and restore runbook](backup-restore.md).
The important notification and maintenance state lives in the same database as
Monitors and results: channels' encrypted configuration, routes, Incidents,
IncidentEvents, outbox rows, attempts, maintenance intervals, retention
leases/cursors, daily aggregates, and audits must remain one consistent
snapshot.

- SQLite online backup may run while workers write; do not copy a live WAL file.
- Local Turso requires a quiesced whole-volume snapshot, so stop Xisnove before
  capturing all database and WAL/state artifacts together.
- Managed Turso recovery and PostgreSQL dumps are provider/database operations.
- Back up the notification keyring separately. A restored database containing
  channels cannot start without every key version still referenced in it.

Restore into a new database, migrate it, and validate readiness before serving.
Also inspect a known maintenance interval, Incident, delivery with attempts,
and daily uptime record where tooling permits. Start only one server during the
initial restore check so no pending delivery is accidentally sent twice.
Decide whether restored pending/retrying deliveries should be allowed to send
before adding replicas or production egress. Xisnove's at-least-once boundary
cannot know whether a provider accepted a request after the backup point.

Restoring an older backup may reintroduce raw rows that retention had already
deleted and may rewind retention leases or delivery state. Workers will resume
from durable state and reclaim expired leases, but operators must account for
possible repeated provider sends and repeated cleanup work.
