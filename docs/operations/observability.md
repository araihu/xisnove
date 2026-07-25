# Observability, placement, and shutdown

The `serve` composition exposes liveness, readiness, Prometheus metrics,
structured JSON logs, optional OpenTelemetry tracing, and an ordered lifecycle
coordinator. Metrics are local to the HTTP server. Trace export is disabled
unless an OTLP/HTTP endpoint is explicitly configured.

## Current health endpoints

`GET /livez` returns HTTP `204` while the HTTP process can handle the request.
It does not test the database or background workers.

`GET /readyz` returns HTTP `204` only while the replica is accepting work and
the configured database profile responds with the exact required schema
version. It returns an HTTP `503`
`application/problem+json` response otherwise. Startup performs the same
database readiness check and validates that the configured notification
keyring can open all stored channel key versions before listening.

```bash
curl -fsS -o /dev/null "$XISNOVE_URL/livez"
curl -fsS -o /dev/null "$XISNOVE_URL/readyz"
```

On shutdown, readiness fails before claim loops are canceled. New Agent work
long-polls are rejected, and existing long-polls are canceled before ordinary
in-flight work drains.

`GET /metrics` exposes the process-local Prometheus registry. Keep it on a
private listener or protect it at the deployment ingress; Xisnove does not add
authentication to operational paths.

## Current logs

HTTP middleware assigns or preserves `X-Request-ID`, returns it in the response,
and logs method, path, duration, correlation ID, and active trace/span IDs
through a JSON `slog` handler. Panic logs include contextual IDs and a stack.
Background loops run inside fixed-name worker spans and report stable messages
and coarse error classes for scheduler, staleness, notification delivery,
maintenance projection, and retention failures. Keyring startup logs only
whether it is configured and the active version; rotation logs counts and the
active version.

Sensitive keys, provider diagnostics, the database URL/token, and configured
sensitive values are redacted. Treat logs as sensitive anyway: restrict access
and retention, and never enable provider or database debug output without
checking it for URLs, tokens, payloads, and diagnostics.

Tracing flags are:

- `--tracing-otlp-http-endpoint` (empty disables tracing);
- `--tracing-otlp-insecure` (plaintext export for trusted links only);
- `--tracing-sample-ratio` (closed interval from `0` through `1`);
- `--tracing-export-timeout` (default `10s`).

Alert today on externally observed conditions:

- `/readyz` returning non-204 or the process disappearing;
- repeated `scheduler tick failed` or `staleness tick failed` messages;
- repeated `notification delivery cycle failed`, `maintenance projection cycle
  failed`, or `retention cycle failed` messages;
- growth or age of `pending`/`retrying` deliveries observed through the admin
  delivery API;
- any `permanent-failure` delivery, especially `configuration_unavailable`,
  `channel_unavailable`, `deadline_exceeded`, `provider_retryable`,
  `provider_rejected`, or `attempt_limit_exceeded`;
- Agent heartbeat age and Monitor `unknown` state through existing APIs.

Use bounded delivery queries and the attempt detail described in
[notification operations](notifications.md). Avoid labels derived from Monitor
names, URLs, error strings, provider responses, or tokens in any external
metrics you build.

## Signal contract and current coverage

The registry defines bounded-cardinality metric families for
monitor state/transitions, probes, scheduler cycles and leases, Agent heartbeat
age, duplicate ingestion, outbox age, delivery attempts, pools/transactions,
migrations, and schema version. Real scheduler cycles, probe outcomes and
latency, duplicates, aggregate transitions (including staleness), lease terminal
events, delivery attempts, database pool state, and schema version update the
registry. Absolute heartbeat age, outbox age, monitor counts, transaction
duration, and current lease gauges require aggregate repository queries not yet
present in the storage ports; their families remain unset instead of publishing
misleading partial values. Dashboards should at minimum show readiness, stale Agents,
Monitor state, scheduling lag, oldest due delivery, delivery outcomes/retries,
maintenance projection lag, retention progress/errors, and database
saturation.

Recommended alerts should be based on sustained windows and installation
baselines: readiness failure, no healthy replica, overdue work leases, growing
outbox age, permanent delivery failures, stale Agent heartbeats, retention not
advancing, migration mismatch, and database pool exhaustion. Tune thresholds to
the installation rather than copying a single global value.

## Ordered shutdown behavior

On the first `SIGINT` or `SIGTERM`, or parent-context cancellation, the server:

1. makes readiness fail;
2. rejects new Agent work leases and cancels existing lease long-polls;
3. stops scheduler and worker claim loops;
4. drains admitted delivery, projection, retention, and HTTP claim work;
5. shuts down the HTTP listener and trace exporter;
6. closes the database.

The sequence has a ten-second bound. A second signal forces admitted-work
cancellation and closes the listener. Durable claims still recover after lease
expiry if a process is killed between provider response and final database
commit; because providers generally lack idempotency keys, that boundary can
produce a duplicate external notification. After a forced restart, inspect
delivery attempts and assume a send may have occurred even when final state was
not recorded.

## Hybrid-homelab placement

Place the control plane outside the infrastructure it is expected to diagnose.
A small external VPS or a separate Kubernetes cluster is preferable to the
home cluster being monitored. Keep the database in a failure domain reachable
from the control plane and consistent with the selected
[database profile](database-profiles.md); SQLite and local Turso permit one
active server, while PostgreSQL and managed Turso support multiple stateless
servers after migration.

Place outbound Agents inside private failure domains: the home Kubernetes
cluster, physical-node LAN, VPN-only networks, and isolated VPS segments.
Agents call the control plane and never access its database. Use Cloudflare DNS
only as the public ingress/DNS layer; keep origin authentication and firewall
policy independent so a Cloudflare, home-link, VPN, or cluster failure remains
observable from elsewhere.

Notification egress belongs on the control plane. Public SaaS receivers need no
private CIDR allowance. For a private Alertmanager, prefer a dedicated ingress
reachable through a narrow VPN or firewall rule and allow only its CIDR as
described in [notification operations](notifications.md). Avoid placing both
Xisnove and its only notification receiver behind the same home router, cluster,
VPN concentrator, or DNS dependency.

Keep at least one notification path independent of each monitored domain. For
example, an external control plane can send to a public paging service while an
in-cluster Agent probes private services. Test failure-domain assumptions by
disconnecting the home uplink or VPN and confirming the external control plane
remains ready and can deliver a synthetic operational alert through a safe
test route.
