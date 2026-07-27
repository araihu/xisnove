# Kubernetes control-plane deployment

The `xisnove` chart runs the relational control plane separately from the
infrastructure it monitors. Deploy `xisnove-edge` into monitored clusters; use
this chart in an external VPS, management cluster, or isolated control-plane
cluster. Loss of a monitored cluster must not remove probe history, incidents,
or notification outbox state.

## Required existing Secrets

The chart accepts names and keys only. It never renders a `Secret` or a secret
literal. Kubernetes projects files as mode `0440` for UID/GID `101:101`.

| Values reference | Required keys | Consumer |
|---|---|---|
| `secrets.server.existingSecret` | `cursor-signing-key`, `notification-master-key` | API server |
| `secrets.admin.existingSecret` | `password` | idempotent admin bootstrap |
| `secrets.ui.existingSecret` | `cookie-secret` | UI BFF |
| `database.postgres.existingSecret` | `url` | PostgreSQL server and Jobs |
| `database.tursoManaged.existingSecret` | `url`, `auth-token` | managed Turso server and Jobs |
| `agent.existingSecret` | `credential` | optional public Agent |
| `agent.enrollment.existingSecret` | `token` | optional one-time Agent enrollment init |

Key names are configurable. The cursor key contains at least 32 bytes. The UI
cookie value is base64 encoding of at least 32 decoded bytes. The notification
keyring is the versioned JSON format documented by notification operations.
Bootstrap password and database credentials exist only in mounted files.

External Secrets Operator, Vault/OpenBao agents, or CSI drivers may materialize
the same named references. They must preserve regular-file, group-read-only
permissions and atomic projection semantics. Do not place values in Helm
values, command-line arguments, annotations, or Git.

## Database profiles

### SQLite

`database.profile=sqlite` renders one `StatefulSet`, one RWO
`volumeClaimTemplate`, `OrderedReady` pod management, and exactly one server
replica. Values schema and templates both reject any other replica count.

On first start and replacement, the old pod is terminated before the new pod
can mount the claim. The new pod runs bounded expand migration and idempotent
admin bootstrap init containers, then starts the API. No online migration Job
is rendered. Expect downtime; use a consistent stopped-file backup before an
upgrade. A failed init container leaves the server stopped and the PVC intact,
so rollback means restore the compatible database backup and previous image.

### PostgreSQL and managed Turso

Remote profiles accept one or more stateless server replicas. A Helm
`pre-install,pre-upgrade` Job with weight `-20`, deadline, retry bound, migration
lock timeout, and cleanup policy runs the expand phase before workloads. The
idempotent admin bootstrap Job follows at weight `-10`. Both load the database
URL from an existing Secret file; managed Turso also loads its token from a
file. Neither Job starts probes or notification workers.

Contract migration is never automatic. Run it only after every old process
lease is gone and rollback across the contracted schema is no longer required.

## Install and upgrade

Validate without exposing values:

```bash
helm lint charts/xisnove
helm template xisnove charts/xisnove \
  --namespace xisnove --values my-reference-values.yaml >/tmp/xisnove.yaml
```

Install only after all referenced Secrets exist:

```bash
helm upgrade --install xisnove charts/xisnove \
  --namespace xisnove --create-namespace \
  --values my-reference-values.yaml --wait --timeout 10m
```

For SQLite, scale-down is encoded by the singleton StatefulSet replacement;
never clone or share its RWO claim. For remote profiles, keep old and new
images inside the documented readable schema interval during rolling upgrade.
Migration contention exits retryably and Helm leaves the old workload intact.

## Optional surfaces

- `ingress.enabled` publishes only the UI BFF. TLS references an existing
  certificate Secret. `gateway.enabled` instead renders an `HTTPRoute` attached
  to an existing Gateway; Ingress and Gateway are mutually exclusive.
- `networkPolicy.enabled` limits inbound access by namespace selector. Outbound
  access remains available because server notification transports, remote DBs,
  and Agents need user-specific destinations.
- `pdb.enabled` protects multi-replica remote-profile servers and is rejected
  for SQLite, where it could block the required singleton replacement.
- `serviceMonitor.enabled` selects the API service `/metrics` endpoint.
- resources, node selectors, tolerations, affinity, and topology spread are
  independently configurable for server, UI, and Agent.
- set `serviceAccount.create=false` plus a non-empty name to use an existing
  ServiceAccount. API token automount remains disabled.

The optional Agent runs one outbound replica and exposes its named
`observability` port only inside the cluster. Default mode uses an existing
credential Secret and rereads atomic projection updates.

With `agent.enrollment.enabled=true`, the Agent becomes a one-replica
StatefulSet with a small RWO credential claim. A bounded init container invokes
`xisnove-agent enroll --timeout`, mounting a one-time token from the referenced
Secret and writing the atomic `0600` bundle plus crash-recovery journal on that
same Pod/PVC. Server-side required idempotency replays the identical Agent and
caller-generated credential after a lost response. After success, the token
Secret may be removed: its projection is optional so later pod replacements
reuse the durable bundle before attempting to read the token.

This init-container design is an explicit correction to the original separate
enrollment-Job sketch. A standalone Job cannot portably hand RWO file state to
a concurrently scheduled Deployment: different-node scheduling can cause
Multi-Attach, while a post-install Job deadlocks `helm --wait` because the Agent
cannot become ready first. The chart therefore renders no enrollment Job,
Secret writer, Role, or RoleBinding. Operator materialization or an externally
managed existing Secret remains the alternative.

Server DB/cursor/keyring and UI cookie rotation requires an explicit bounded
rollout restart; the chart never reads Secret data with `lookup` or embeds it
in a rollout annotation.

## Failure checks

Before rollout, verify `/livez` and `/readyz` through their named ports, inspect
the migration/bootstrap Job status for remote profiles, and confirm no rendered
manifest contains a Secret value. On failure, preserve Job logs only after
redaction; database URLs, tokens, passwords, cursor keys, cookie secrets, and
Agent credentials must never enter support artifacts.
