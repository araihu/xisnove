# Milestone 3 Reliable Notifications and Operations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:executing-plans` to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking. Every production behavior follows
> red-green-refactor.

**Goal:** Deliver reliable incident notifications and production operations on
every relational profile, including transactional routing/outbox creation,
encrypted channel configuration, Shoutrrr and Alertmanager delivery, replay,
maintenance suppression, bounded retention, metrics, optional tracing,
readiness, and graceful shutdown.

**Architecture:** Keep notification semantics in domain/application packages
and provider behavior behind ports. The result projector commits health,
Incident, IncidentEvent, audit, route evaluation, and immutable outbox rows in
one database transaction. Replicated workers claim durable rows using database
time and expiring leases, perform network I/O outside transactions, then record
every attempt. SQLite, local Turso, managed Turso, and PostgreSQL implement the
same repository behavior. Kubernetes Jobs and Kubernetes API persistence are
not part of notification delivery.

Before Task 3 adds more infrastructure ports, the Open Core compatibility gate
promotes the already-completed domain behavior, application services, ports,
and adapter conformance suites into importable public packages. Tasks 1 and 2
were initially landed under `internal`; Task 2.5 performs that promotion and
all later task paths refer to the resulting public layout.

**Tech Stack:** Go 1.26.1, OpenAPI 3.1.2, oapi-codegen v2.8.0, sqlc v1.31.1,
Goose v3.27.3, `github.com/nicholas-fedor/shoutrrr` v0.16.2, Prometheus client
library, and OpenTelemetry Go SDK/exporter packages pinned when Task 12 starts.

## Global constraints

- The public OpenAPI document remains canonical; generated server and SDK
  artifacts are reviewed and committed.
- The canonical module remains `github.com/araihu/xisnove`. Public extension
  packages are `domain`, `application`, `application/port`, and `contracttest`;
  self-hosted implementations remain under `internal/adapters`.
- Every application/port operation accepts `context.Context` first and
  propagates the incoming context. The public UnitOfWork exposes `View` and
  `Transact`, and passes the same context to transaction-scoped callbacks.
- Public packages remain free of SaaS tenancy, billing, subscription, and
  entitlement concepts. Future OLAP integration uses a separate analytics
  port and cannot replace operational transaction semantics.
- Monitor `description`, `labels`, display order, and public-page selection are
  implemented now because routing and the milestone 4 status page consume
  them. Labels are copied into render snapshots so replay is historically
  stable.
- Notification route evaluation, template selection, render input, dedupe key,
  and maintenance decision are pure domain behavior with explicit time.
- Incident state, IncidentEvent, audit event, and required outbox rows commit
  atomically. A provider failure never rolls back, hides, or mutates Incident
  history.
- An outbox row is immutable except for claim/delivery state. Its uniqueness key
  is `(incident_event_id, route_id, channel_id)` on every database profile.
- Every delivery attempt is durable. Claims use database time, short
  transactions, compare-and-set updates, bounded leases, and crash recovery.
- Network calls occur outside transactions with bounded concurrency, worker
  deadlines, injected HTTP timeouts, and an explicit egress policy.
- Channel secrets are encrypted with versioned AEAD. The master-key keyring is
  loaded from a file or environment-backed file path and never persisted,
  logged, returned by the API, or placed in command arguments.
- V1 secret references resolve through files. The application port permits
  future ESO, Vault, and OpenBao materialization without direct provider APIs in
  this milestone.
- Maintenance suppresses notification rows while probes and health projection
  continue. Ending maintenance emits one fresh unhealthy transition when
  required; recurring schedules remain rejected.
- Retention and aggregation operate in bounded batches and never delete
  Incident, IncidentEvent, associated attempt, or audit history by default.
- All four storage profiles run the same behavioral journey. Managed Turso uses
  only a dedicated deletion-enabled CI group; the protected `konclave-ci` group
  is never modified by tests.
- Service shutdown first fails readiness and stops claims/long-polls, then
  drains bounded in-flight work before closing listeners and persistence.

---

### Task 1: Notification domain and monitor routing metadata

**Files:**
- Create: `internal/domain/notification.go`
- Create: `internal/domain/notification_test.go`
- Create: `internal/domain/maintenance.go`
- Create: `internal/domain/maintenance_test.go`
- Modify: `internal/domain/monitor.go`
- Modify: `internal/domain/monitor_test.go`

**Interfaces:**
- Produces: `NotificationChannel`, `NotificationRoute`, `NotificationEvent`
- Produces: `RenderSnapshot`, `DeliveryState`, `RetryDecision`
- Produces: `MaintenanceInterval`, `MaintenanceDecision`
- Extends: `Monitor` with description, labels, display order, and public flag

- [x] **Step 1: Write failing monitor metadata tests**

Prove description trimming, label key/value validation, deterministic cloned
label maps, non-negative display order, and unchanged defaults for existing
monitor constructors. Valid label keys use the Kubernetes label-name character
shape without importing Kubernetes packages.

- [x] **Step 2: Write failing routing and identity tests**

Table tests cover exact label matchers, event actions (`open`, `change`,
`recover`, and synthetic `maintenance-ended`), warning/critical filters,
disabled routes/channels, deterministic ordering, and a stable dedupe identity
derived from event ID, route ID, and channel ID.

- [x] **Step 3: Write failing retry and maintenance truth tables**

Prove capped exponential retry with injected jitter, permanent versus transient
classification, one-off `[start,end)` intervals, indefinite intervals, invalid
ranges, and the fresh-transition decision after maintenance ends while health
is unhealthy.

- [x] **Step 4: Implement pure domain behavior**

Do not import storage, HTTP, template engines, crypto, or clocks. Use explicit
`time.Time` and injected random values. Clone every map/slice crossing an entity
boundary.

- [x] **Step 5: Verify and commit**

```bash
go test -race ./internal/domain -run 'MonitorMetadata|Notification|Route|Retry|Maintenance'
git add internal/domain
git commit -m "feat(notification): define routing domain"
```

---

### Task 2: Relational notification and operations schema

**Files:**
- Create: `db/migrations/sqlite/00004_notifications.sql`
- Create: `db/migrations/postgres/00004_notifications.sql`
- Modify: `db/migrations/sqlite/migrations.go`
- Modify: `db/migrations/postgres/migrations.go`
- Create: `db/queries/sqlite/notifications.sql`
- Create: `db/queries/postgres/notifications.sql`
- Create: `db/queries/sqlite/maintenance.sql`
- Create: `db/queries/postgres/maintenance.sql`
- Create: `db/queries/sqlite/retention.sql`
- Create: `db/queries/postgres/retention.sql`
- Modify: `db/queries/sqlite/configuration.sql`
- Modify: `db/queries/postgres/configuration.sql`
- Generate: `db/generated/sqlite/*.go`
- Generate: `db/generated/postgres/*.go`
- Modify: `db/migrations/postgres/schema_test.go`
- Modify: `integration/migration_upgrade_test.go`

**Schema:**

- `notification_channels`: ID, name, kind, encrypted configuration envelope,
  key version, enabled flag, timestamps.
- `notification_routes`: ID, name, channel ID, optional monitor ID, label/event/
  severity matcher JSON, template, enabled flag, precedence, timestamps.
- `notification_outbox`: ID, IncidentEvent/route/channel IDs, unique dedupe key,
  immutable render snapshot JSON, state, available time, claim owner/token hash/
  expiry, attempt count, last diagnostic class, delivered/suppressed timestamps,
  created timestamp.
- `notification_delivery_attempts`: ID, outbox ID, ordinal, start/finish,
  outcome, error class, bounded diagnostic, provider receipt; unique ordinal per
  outbox row.
- `maintenance_intervals`: ID, monitor ID, start, nullable end, reason,
  creator/timestamps.
- `audit_events`: ID, event kind, subject kind/ID, optional Incident ID,
  immutable payload JSON, timestamp.
- `daily_uptime`: monitor/location/day key plus passing/failing/unknown counts
  and observed duration.
- Monitor columns for description, labels JSON, display order, and public flag.

- [x] **Step 1: Add failing schema/upgrade assertions**

Assert every table, foreign key, uniqueness rule, due-claim index, retention
index, monitor metadata default, and migration version. Upgrade a version-3
fixture containing an active Incident and prove it remains readable.

- [x] **Step 2: Author semantically equivalent migrations**

SQLite-compatible timestamps remain canonical UTC RFC3339 text and comparisons
use `julianday`; PostgreSQL uses `timestamptz`, JSONB, and partial indexes where
appropriate. Down migrations remove only version-4 additions.

- [x] **Step 3: Add query families**

Provide CRUD, deterministic route listing, atomic due claims, attempt append,
delivery/retry/permanent/suppressed CAS updates, replay reset, active
maintenance lookup, ended-maintenance claim, daily aggregation upsert, and
bounded deletion. PostgreSQL claims use `FOR UPDATE SKIP LOCKED`; compatible
profiles use one-statement candidate/update CAS.

- [x] **Step 4: Generate and verify**

```bash
go tool sqlc generate
go tool sqlc diff
go test -race ./db/migrations/postgres ./integration -run 'Migration'
git diff --check
```

- [x] **Step 5: Commit**

```bash
git add db sqlc.yaml integration/migration_upgrade_test.go
git commit -m "feat(notification): add relational outbox schema"
```

---

### Task 2.5: Open Core extension-surface gate

**Plan:** `docs/superpowers/plans/2026-07-25-open-core-extension-surface.md`

**Files:**
- Move: `internal/domain/**` to `domain/**`
- Move: `internal/application/**` to `application/**`
- Create: `application/port/**`
- Move: `internal/adapters/conformance/**` to `contracttest/**`
- Create: `internal/architecture/dependencies_test.go`
- Create: `integration/testdata/external-module/go.mod`
- Create: `integration/testdata/external-module/external_test.go`
- Create: `integration/external_module_test.go`
- Modify: all imports and composition roots

**Acceptance:**

- [x] The canonical module path remains `github.com/araihu/xisnove`.
- [x] Domain, application services, infrastructure ports, and adapter contract
  suites are importable by another module without access to `internal`.
- [x] The coarse public UnitOfWork preserves atomic `View`/`Transact`
  boundaries and supplies the caller context to callbacks.
- [x] No application service replaces an incoming context with
  `context.Background()`.
- [x] An architecture test enforces dependency direction and keeps operational
  persistence separate from future analytical ports.
- [x] An external-module fixture passes with `GOWORK=off`, imports all four
  public package roots, supplies a fake adapter, and constructs a service.
- [x] Public-package and OpenAPI compatibility follow semantic versioning; the
  release process cannot claim Apache 2.0 until `LICENSE` and required notices
  are present.
- [x] The core remains a complete single-tenant self-hosted product and gains
  no SaaS tenant, billing, subscription, or entitlement concepts.

Implement and verify this gate using the dedicated plan before resuming Task 3.

---

### Task 3: Profile-neutral repositories and conformance

**Files:**
- Modify: `application/port/store.go`
- Modify: `internal/adapters/sqlitecompat/store.go`
- Modify: `internal/adapters/postgres/store.go`
- Create: `contracttest/notifications.go`
- Modify: `internal/adapters/sqlite/conformance_test.go`
- Modify: `internal/adapters/tursolocal/conformance_test.go`
- Modify: `internal/adapters/tursocloud/conformance_test.go`
- Modify: `internal/adapters/postgres/conformance_test.go`

**Interfaces:**
- Produces: `NotificationChannelRepository`, `NotificationRouteRepository`
- Produces: `NotificationOutboxRepository`, `MaintenanceRepository`
- Produces: `AuditRepository`, `RetentionRepository`
- Extends: `Repositories` so transaction-scoped instances include every port

- [x] **Step 1: Write the shared failing conformance journey**

The same assertions cover channel/route round trips without plaintext secrets,
route ordering, duplicate outbox prevention, transaction rollback, competing
claims, database-time lease expiry, immutable snapshot reads, attempt ordinals,
retry/permanent/delivered/suppressed transitions, replay, maintenance claims,
daily aggregate upsert, and bounded cleanup.

- [x] **Step 2: Implement SQLite-compatible mapping**

Map generated types at the boundary, preserve UTC/JSON normalization, hash
claim tokens, and classify semantic conflicts. No generated or driver type may
enter application/domain packages.

- [x] **Step 3: Implement PostgreSQL mapping**

Match the same application behavior with pgx/sqlc types and native claims.
Prove two independent handles cannot claim one delivery or ended interval.

- [x] **Step 4: Run the local matrix repeatedly**

```bash
go test -race ./internal/adapters/sqlite ./internal/adapters/tursolocal \
  ./internal/adapters/postgres -run 'NotificationConformance' -count=20
```

- [x] **Step 5: Commit**

```bash
git add application/port contracttest internal/adapters
git commit -m "feat(notification): persist durable delivery state"
```

---

### Task 4: Versioned encryption and secret resolution

**Files:**
- Create: `application/secrets.go`
- Create: `application/secrets_test.go`
- Create: `internal/adapters/crypto/envelope.go`
- Create: `internal/adapters/crypto/envelope_test.go`
- Create: `internal/adapters/secrets/file.go`
- Create: `internal/adapters/secrets/file_test.go`
- Modify: `cmd/xisnove-server/main.go`
- Modify: `cmd/xisnove-server/serve.go`
- Create: `cmd/xisnove-server/notification_keys.go`
- Create: `cmd/xisnove-server/notification_keys_test.go`

**Interfaces:**
- Produces: `ConfigSealer` with versioned `Seal`/`Open`
- Produces: `SecretResolver.Resolve(context.Context, SecretReference)`
- Consumes: a master-key keyring file containing active version and base64 keys

- [x] **Step 1: Write failing cryptographic-envelope tests**

Use AES-256-GCM with random nonces and associated data binding channel ID,
kind, and envelope version. Prove ciphertext differs for equal
plaintext, tampering/wrong identity fails closed, old versions decrypt after
rotation, and new writes use only the active version.

- [x] **Step 2: Write failing keyring/file-resolution tests**

Reject absent, malformed, duplicate-version, short-key, symlink, non-regular,
and overly permissive key files. Bound secret size, trim one final newline only,
and never include content in an error. The file resolver is the v1
implementation; the port remains provider-neutral for ESO/Vault/OpenBao.

- [x] **Step 3: Implement keyring loading and sealing**

Accept `--notification-master-key-file`/`XISNOVE_NOTIFICATION_MASTER_KEY_FILE`.
Configuration diagnostics expose only whether a keyring is configured and its
active version. Startup refuses configured notification channels when their key
versions cannot be opened.

- [x] **Step 4: Add rotation support**

Provide a bounded application operation and server subcommand that re-encrypts
channel configurations in batches, is restart-safe, and keeps old keys until
no row references them.

- [x] **Step 5: Verify and commit**

```bash
go test -race ./internal/adapters/crypto ./internal/adapters/secrets \
  ./application ./internal/config ./cmd/xisnove-server
git add application internal cmd
git commit -m "feat(notification): encrypt channel configuration"
```

---

### Task 5: Transactional incident routing and outbox creation

**Files:**
- Create: `application/notifications.go`
- Create: `application/notifications_test.go`
- Modify: `application/projection.go`
- Modify: `application/results_test.go`
- Modify: `application/staleness_test.go`
- Modify: `domain/incident.go`

**Interfaces:**
- Produces: one transaction-scoped `RecordIncidentTransition` orchestration
- Consumes: monitor, maintenance, route, channel, Incident, audit, and outbox
  repositories

- [x] **Step 1: Write failing transaction tests**

For open/change/recover transitions prove health, Incident, IncidentEvent,
audit, and all matching outbox rows commit together. Inject failure after each
write and assert complete rollback. Replaying the same projection creates no
duplicate event or outbox row.

- [x] **Step 2: Write routing snapshot tests**

Snapshots include event identity/action/state/severity/times, Incident and
Monitor identity/name/description/labels, route template/version, and selected
channel kind. They contain no decrypted channel URL/token. Later label or
template edits do not alter existing snapshots.

- [x] **Step 3: Implement maintenance suppression in the transaction**

When active maintenance matches, persist the IncidentEvent and audit decision
but create outbox rows directly in `suppressed` state. Health and Incident
history continue normally.

- [x] **Step 4: Verify all projection paths**

```bash
go test -race ./application -run 'Result|Projection|Stale|Notification'
```

- [x] **Step 5: Commit**

```bash
git add application domain
git commit -m "feat(notification): enqueue incident transitions atomically"
```

---

### Task 6: Public notification and maintenance API

**Files:**
- Modify: `api/openapi.yaml`
- Modify: `api/contract_test.go`
- Generate: `sdk/generated.gen.go`
- Generate: `internal/adapters/httpapi/generated.gen.go`
- Create: `internal/adapters/httpapi/notifications.go`
- Create: `internal/adapters/httpapi/notifications_test.go`
- Create: `internal/adapters/httpapi/maintenance.go`
- Create: `internal/adapters/httpapi/maintenance_test.go`
- Modify: `internal/adapters/httpapi/server.go`

**Operations:**
- Channel create/list/get/update/disable without secret reflection.
- Route create/list/get/update/disable with typed label/event/severity filters.
- Delivery list/get and explicit manual replay of permanent failures.
- Maintenance create/list/get/delete/end for one-off/indefinite intervals.
- Monitor representations include description, labels, display order, public
  selection.

- [x] **Step 1: Add failing OpenAPI contract assertions**

Require OpenAPI 3.0.3 (the latest version officially supported by the pinned
`oapi-codegen`) with discriminated channel configuration inputs, `writeOnly`
secret-bearing fields, redacted response schemas, bounded pagination, stable
delivery states, RFC 9457 errors, and admin authorization on every operation.

- [x] **Step 2: Extend the canonical contract and regenerate**

```bash
go generate ./...
go tool vacuum lint -d api/openapi.yaml
go test ./api ./sdk
```

- [x] **Step 3: Implement strict handlers**

Handlers only map generated HTTP types to application commands/queries. Never
log request bodies for channel writes. Replay requires an explicit delivery ID
and records an audit event.

- [x] **Step 4: Verify generated drift and commit**

```bash
git add api sdk internal/adapters/httpapi
go generate ./...
git diff --exit-code
go test -race ./api ./sdk ./internal/adapters/httpapi
git commit -m "feat(api): expose notification operations"
```

---

### Task 7: Notification transport boundary and egress policy

**Files:**
- Create: `application/transport.go`
- Create: `application/transport_test.go`
- Create: `internal/adapters/egress/policy.go`
- Create: `internal/adapters/egress/policy_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Produces: `NotificationTransport.Send(context.Context, Delivery) Result`
- Produces: stable transient/permanent error classes and provider receipt
- Produces: DNS/IP/redirect-aware egress policy shared by transports

- [x] **Step 1: Pin and audit Shoutrrr v0.16.2**

Pin `github.com/nicholas-fedor/shoutrrr@v0.16.2`. Record supported schemes that
honor injected HTTP behavior. Reject schemes that cannot satisfy timeout and
egress guarantees until a reviewed adapter exists.

- [x] **Step 2: Write failing egress tests**

Default policy blocks loopback, link-local, multicast, unspecified, private,
carrier-grade NAT, Kubernetes service ranges when configured, and DNS rebinding.
Explicit allow rules support homelab destinations. Every redirect and resolved
address is revalidated.

- [x] **Step 3: Implement the provider-neutral boundary**

Context cancellation and deadlines belong to Xisnove even if a provider API
lacks context support. Diagnostics are bounded and scrubbed of credentials,
URLs with userinfo/query secrets, headers, and message payloads.

- [x] **Step 4: Verify and commit**

```bash
go test -race ./application ./internal/adapters/egress
git add internal go.mod go.sum
git commit -m "feat(notification): define transport boundary"
```

---

### Task 8: Shoutrrr and Alertmanager adapters

**Files:**
- Create: `internal/adapters/shoutrrr/transport.go`
- Create: `internal/adapters/shoutrrr/transport_test.go`
- Create: `internal/adapters/alertmanager/transport.go`
- Create: `internal/adapters/alertmanager/transport_test.go`

- [x] **Step 1: Write Shoutrrr contract tests**

Use `CreateSenderWithOptions`/`SenderOptions` with an injected HTTP client and
timeout. Prove allowed schemes, context deadline behavior, provider error
classification, secret scrubbing, template payload preservation, and bounded
parallel calls against local test servers.

- [x] **Step 2: Implement the reviewed Shoutrrr subset**

Xisnove owns retries and never enables an independent provider retry loop.
Configuration URLs exist only in decrypted call-local memory and are never put
into snapshots or diagnostics.

- [x] **Step 3: Write and implement Alertmanager semantics**

POST `/api/v2/alerts` using firing/resolved alerts, stable fingerprints, RFC3339
start/end times, Xisnove labels/annotations, injected client, authorization
reference, and egress policy. Treat 2xx as success, retry 408/425/429/5xx and
transport failures, and classify other 4xx as permanent.

- [x] **Step 4: Verify and commit**

```bash
go test -race ./internal/adapters/shoutrrr ./internal/adapters/alertmanager
git add internal/adapters
git commit -m "feat(notification): add delivery transports"
```

---

### Task 9: Durable notification worker and replay

**Files:**
- Create: `application/delivery_worker.go`
- Create: `application/delivery_worker_test.go`
- Modify: `cmd/xisnove-server/serve.go`
- Modify: `cmd/xisnove-server/serve_test.go`

- [x] **Step 1: Write failure-first worker tests**

Cover due claiming, bounded concurrency, network outside transactions, success,
transient retry with cap/jitter, permanent failure, deadline, panic containment,
lease loss, crash after claim, crash after provider response, duplicate-provider
risk visibility, clean stop, and manual replay producing a new attempt ordinal.

- [x] **Step 2: Implement claims and dispatch**

Each loop reads database time, claims a bounded batch, decrypts/resolves
configuration just in time, chooses transport by channel kind, sends under a
deadline, then records the attempt and CAS state. A stale worker cannot finalize
a row after its claim token/lease is lost.

- [x] **Step 3: Wire lifecycle/configuration**

Expose bounded batch, concurrency, lease, poll, send-timeout, max-attempt, and
backoff cap configuration with safe defaults and validation. Every eligible
replica may run the worker.

- [x] **Step 4: Verify and commit**

```bash
go test -race ./application ./cmd/xisnove-server \
  -run 'Delivery|NotificationWorker|Shutdown'
git add application cmd/xisnove-server
git commit -m "feat(notification): deliver durable outbox work"
```

---

### Task 10: Maintenance lifecycle

**Files:**
- Create: `application/maintenance.go`
- Create: `application/maintenance_test.go`
- Modify: `cmd/xisnove-server/serve.go`
- Modify: `integration/storage_journey_test.go`

- [ ] **Step 1: Test create/end/delete invariants**

Reject recurring fields, end-before-start, edits that rewrite elapsed history,
and unauthorized deletion. Ending/deleting an active interval is idempotent and
audited.

- [ ] **Step 2: Test the maintenance-end projector**

Claim ended intervals durably. If monitor health remains degraded/down/unknown,
append exactly one synthetic `maintenance-ended` IncidentEvent plus matching
audit/outbox in a transaction. If recovered, emit nothing. Competing replicas
cannot duplicate the transition.

- [ ] **Step 3: Implement and wire the worker**

Use database time and bounded claims. Starting or stopping the server must not
lose the post-maintenance transition.

- [ ] **Step 4: Verify and commit**

```bash
go test -race ./application ./integration -run 'Maintenance'
git add application cmd/xisnove-server integration
git commit -m "feat(maintenance): preserve unhealthy transitions"
```

---

### Task 11: Daily uptime aggregation and bounded retention

**Files:**
- Create: `application/retention.go`
- Create: `application/retention_test.go`
- Modify: `cmd/xisnove-server/serve.go`
- Create: `integration/retention_test.go`

- [ ] **Step 1: Write aggregation truth tests**

Aggregate immutable results into UTC daily buckets, make reruns idempotent, and
handle late results without double counting. Preserve enough state to resume a
partially completed day safely.

- [ ] **Step 2: Write cleanup boundary tests**

Defaults are raw results 30 days and daily uptime 13 months. Exact cutoff rows
are retained consistently. Each transaction deletes at most configured batch
size. Incident/Event/attempt/audit history remains untouched by default.

- [ ] **Step 3: Implement lease-safe jobs**

Only one replica owns a bucket/batch at a time; expired claims recover. Record
job audit/metrics without placing per-row payloads in logs.

- [ ] **Step 4: Verify and commit**

```bash
go test -race ./application ./integration -run 'Retention|Uptime'
git add application cmd/xisnove-server integration
git commit -m "feat(retention): aggregate and prune bounded history"
```

---

### Task 12: Metrics, tracing, readiness, and structured logs

**Files:**
- Create: `internal/adapters/observability/metrics.go`
- Create: `internal/adapters/observability/metrics_test.go`
- Create: `internal/adapters/observability/tracing.go`
- Create: `internal/adapters/observability/logging.go`
- Modify: `cmd/xisnove-server/serve.go`
- Modify: `cmd/xisnove-server/serve_test.go`
- Modify: `internal/adapters/httpapi/server.go`
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Pin current stable observability libraries**

Review and pin Prometheus client and OpenTelemetry SDK/exporter versions. Traces
are disabled unless explicitly configured; metrics remain local and do not
require an external collector.

- [ ] **Step 2: Write endpoint and cardinality tests**

`/livez` reports process health. `/readyz` requires accepting state, database
ping, and exact schema compatibility. `/metrics` contains monitor-state,
transition, probe, scheduler, lease, heartbeat-age, duplicate, outbox-age,
attempt, pool, transaction, and migration measures without monitor names, URLs,
error strings, tokens, or other unbounded labels.

- [ ] **Step 3: Implement request/log/trace correlation**

Emit JSON logs with correlation and applicable run/monitor/location/agent/
incident/delivery IDs. Propagate W3C trace context through HTTP and workers.
Redact configured sensitive values and provider diagnostics.

- [ ] **Step 4: Verify and commit**

```bash
go test -race ./internal/adapters/observability ./internal/adapters/httpapi \
  ./cmd/xisnove-server
git add internal cmd go.mod go.sum
git commit -m "feat(operations): expose service telemetry"
```

---

### Task 13: Graceful shutdown and fault recovery

**Files:**
- Modify: `cmd/xisnove-server/serve.go`
- Modify: `cmd/xisnove-server/serve_test.go`
- Create: `integration/worker_recovery_test.go`

- [ ] **Step 1: Add ordered-shutdown tests**

Assert readiness fails before claims stop, new long-polls are rejected, bounded
in-flight delivery/projection/retention work drains, safe leases are released,
expired leases recover, then listeners and DB close. A second signal forces a
bounded exit.

- [ ] **Step 2: Add process fault tests**

Terminate workers between claim/send/finalize boundaries and prove another
server resumes after lease expiry. Run two server instances against PostgreSQL
and compatible multi-replica managed Turso where protected credentials exist.

- [ ] **Step 3: Implement a single lifecycle coordinator**

Avoid unrelated goroutine ownership in adapters. The server owns accepting,
claiming, draining, and close phases and reports them in readiness/logs.

- [ ] **Step 4: Verify and commit**

```bash
go test -race ./cmd/xisnove-server ./integration -run 'Shutdown|WorkerRecovery'
git add cmd/xisnove-server integration
git commit -m "feat(operations): drain workers gracefully"
```

---

### Task 14: Cross-profile notification journey and release gate

**Files:**
- Modify: `integration/storage_journey_test.go`
- Modify: `integration/storage_matrix_test.go`
- Create: `integration/notification_journey_test.go`
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/turso-cloud-integration.yml`
- Modify: `Makefile`
- Modify: `README.md`
- Create: `docs/operations/notifications.md`
- Create: `docs/operations/maintenance-retention.md`
- Create: `docs/operations/observability.md`

- [ ] **Step 1: Extend the literal shared storage journey**

Run identical operations/assertions for SQLite, local Turso, PostgreSQL, and
managed Turso: metadata round trip, channel encryption, route matching,
Incident transaction/outbox, duplicate prevention, two-handle claims, failed
attempt/retry, permanent failure/replay, success, maintenance suppression/end,
retention batches, migration, close/reopen, and rollback injection.

- [ ] **Step 2: Add transport-level end-to-end tests**

Use local HTTP receivers to prove Shoutrrr and Alertmanager payloads, timeout,
retry classes, resolution semantics, and no secret leakage. These tests use the
public SDK for setup and queries rather than direct handler calls. Use
Testcontainers for OCI-backed server components and auxiliary dependencies so
the suite self-scaffolds and tears down its environment. Preserve explicit
external-service overrides for debugging and CI environments that provide their
own dependencies.

- [ ] **Step 3: Wire CI gates**

Normal CI runs SQLite/local Turso plus ephemeral PostgreSQL, module-isolated
tests, generated drift, race tests, and notification fault tests. Protected CI
creates and tears down one managed Turso database only in a deletion-enabled
group and uploads JUnit. Release gate requires all four profiles.
Local PostgreSQL integration and E2E tests self-provision PostgreSQL 18 through
Testcontainers when a healthy container runtime is available; the explicit
`XISNOVE_TEST_POSTGRES_URL` override remains supported for CI and debugging.
Managed Turso remains a distinct protected profile provisioned and torn down
through the real Turso Platform API rather than emulated in a container.

- [ ] **Step 4: Document exact operations**

Document channel keyring creation/rotation, file secret references, egress
allow rules, Alertmanager integration, delivery inspection/replay, maintenance,
retention tuning, dashboards/alerts, readiness, shutdown, backup interaction,
and external-control-plane placement for the hybrid homelab.

- [ ] **Step 5: Run the milestone gate**

```bash
go mod tidy
go work sync
git add go.mod go.sum agent/go.mod agent/go.sum go.work.sum
make check
go test -race ./integration -run 'NotificationJourney|WorkerRecovery' -count=10
git diff --check
git status --short
```

Expected: local gates pass and the worktree contains only intentional staged
generated/documentation changes. Managed Turso reports an explicit credential
skip locally unless the dedicated deletion-enabled group is configured.

- [ ] **Step 6: Commit and push the milestone**

```bash
git add .github Makefile README.md docs integration
git commit -m "docs(notification): complete operations runbooks"
git push
```

---

## Milestone 3 completion evidence

Milestone 3 is complete only when all of the following are directly proven:

- The canonical OpenAPI and generated SDK can configure redacted channels,
  routes, deliveries/replay, maintenance, and monitor routing metadata.
- A fault-injected integration test proves health, Incident, IncidentEvent,
  audit, and matching immutable outbox rows are one transaction.
- The same conformance journey passes on SQLite, local Turso, PostgreSQL, and a
  disposable managed Turso database.
- Competing replicas and crash recovery cannot double-claim durable work;
  provider calls remain at-least-once and duplicate-provider risk is visible in
  attempt/audit history.
- Shoutrrr v0.16.2 and Alertmanager pass bounded timeout, egress, error-class,
  redaction, firing, and resolved behavior tests.
- Maintenance suppression and the one-time post-maintenance unhealthy event are
  proven under competing workers.
- Retention defaults, daily aggregation, exact cutoff, and bounded cleanup are
  proven without deleting protected history.
- `/livez`, `/readyz`, `/metrics`, optional traces, structured redacted logs,
  and ordered graceful shutdown are exercised by tests.
- `make check`, module-isolated checks, race/fault suites, generation-drift
  checks, CI configuration, and operations runbooks are green and current.
