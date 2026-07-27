# Runtime contracts

Deployment artifacts consume this contract. They must not invent alternate
ports, probes, writable paths, or shutdown budgets. Raw defaults bind loopback;
container resources explicitly bind the documented wildcard address.

| Deployable | Bind and named ports | Live / ready / metrics | Authentication and response | Readiness | Shutdown | Writable paths |
|---|---|---|---|---|---|---|
| `xisnove-server` | raw `127.0.0.1:8080`, container `0.0.0.0:8080`, `http` | `/livez`, `/readyz`, `/metrics` on `http` | probes unauthenticated, `204` or `503`; Prometheus text unauthenticated inside deployment boundary; API uses session or scoped token | HTTP accepting, DB ping succeeds, schema in supported interval, migration fence open | `SIGINT`/`SIGTERM`, stop claims, drain 10s, close DB | `/var/lib/xisnove` only for SQLite/local Turso; none for remote DB profiles |
| `xisnove-ui` | raw `127.0.0.1:8081`, container `0.0.0.0:8081`, `http` | `/livez`, `/readyz`; no `/metrics` in v1 | probes unauthenticated, `204` or `503`; normal routes retain cookie/session and CSRF policy | listener accepting and configured API origin valid; API outage remains page-level degraded state | `SIGINT`/`SIGTERM`, drain 10s | none; cookie secret is mounted read-only |
| `xisnove-agent` | raw `127.0.0.1:9090`, container `0.0.0.0:9090`, `observability` | `/livez`, `/readyz`, `/metrics` on `observability` | probes and Prometheus text unauthenticated inside deployment boundary; no public control API | credential loaded, control-plane client initialized, probe workers accepting leases | `SIGINT`/`SIGTERM`, stop lease claims, drain 10s | `/var/lib/xisnove-agent` for owner-only materialized credential only |
| `xisnove-operator` | `0.0.0.0:8080` `metrics`; `0.0.0.0:8081` `health` | `/healthz` and `/readyz` on `health`; `/metrics` on `metrics` | unauthenticated inside cluster NetworkPolicy boundary | manager elected when enabled, cache synced, control-plane dependencies initialized | `SIGINT`/`SIGTERM`, controller-runtime graceful shutdown 30s | none; provisioning credential mounted read-only |
| `xisnove` CLI | no listener | no probe or metrics endpoint | API session/scoped token from keyring or owner-only file | not applicable | interrupt cancels request; command-specific bounded timeout | platform config directory only when login writes profile; token uses OS keyring by default |

HTTP servers use a 5s read-header timeout. Outbound UI, Agent, and operator
requests have explicit bounded timeouts; no health endpoint waits on an
unbounded remote call. Kubernetes probes address named ports.

Each service follows `starting -> ready -> draining -> stopped`. `/readyz`
returns `503` while starting or draining and `204` only in ready state.
Dependency loss moves ready services to degraded/not-ready without making
`/livez` wait on that dependency. `/livez` returns within one second while the
process event loop can serve requests. Shutdown first makes readiness fail,
then rejects new work, drains within its budget, and exits.

Each server process acquires a database-backed version lease after database
readiness, heartbeats it every 15 seconds with a 45-second TTL, and releases it
during clean shutdown. `--installation-id` namespaces leases when one remote
database contains more than one installation. Heartbeat failure makes
`/readyz` fail and initiates shutdown; contract migration cannot pass a live
incompatible lease.

All five binaries implement `--version` without initializing dependencies.
`xisnove-server db migrate` and `admin bootstrap` expose an overall `--timeout`
that bounds secret resolution, database open, readiness, and the complete
operation; migration's `--lock-timeout` remains the narrower admission bound.
`xisnove-agent enroll` accepts only file-backed enrollment input, persists its
caller credential and idempotency journal before network mutation, and writes
the final credential bundle atomically.
Stable output is one line:

```text
<binary> version=X.Y.Z commit=<40-hex> build_date=<RFC3339-UTC> dirty=false
```

Success exits `0`. Invalid version metadata or malformed flags exit `2` and
write a single diagnostic to stderr. `--version` never writes files or emits
credentials.
