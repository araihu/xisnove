# Milestone 2A Protocol Breadth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the verified first-observation path so HTTP/TLS, TCP, and DNS monitors execute through the same public API, scheduler, outbound Agent, idempotent result pipeline, health projection, and Incident model, with bounded scheduler recovery and stale-location detection.

**Architecture:** Preserve the existing hexagonal boundaries and relational coordination model. The public OpenAPI contract gains a discriminated `ProbeDefinition` union; domain and application code own typed probes and scheduling decisions; SQLite persists the union as kind plus canonical JSON; the independently buildable Agent selects a protocol executor from the leased union without importing root internals. This plan deliberately covers milestone 2 protocol behavior only; PostgreSQL, local Turso, managed Turso, and their conformance suite are the separate milestone 2B plan.

**Tech Stack:** Go 1.26.1, OpenAPI 3.1.2, `oapi-codegen` v2.8.0, sqlc v1.31.1, Goose v3.27.3, `modernc.org/sqlite`, standard-library `net`, `net/http`, `net/netip`, `crypto/tls`, and `github.com/miekg/dns` v1.1.72.

## Global Constraints

- `api/openapi.yaml` remains the canonical application and Agent contract; generated root SDK/server and Agent clients must remain deterministic.
- Domain packages must not import HTTP clients, SQL drivers, generated OpenAPI/sqlc types, or Agent packages.
- Every new production behavior follows red-green-refactor; generated files and declarative migration/CI files are the only test-first exceptions.
- Agents remain outbound-only, use no database credentials, and never persist credentials, leased secrets, or results.
- TCP and DNS targets use the same explicit private CIDR allow-list and unconditional cloud-metadata/link-local denial as HTTP.
- A retry, expired lease, duplicate scheduler tick, or duplicate result must not cause a second projection or Incident transition.
- Initial `pending` state does not notify; post-grace stale required locations roll up to `unknown`.
- Catch-up creates at most one current run per Monitor/Location per scheduler tick; skipped intervals are counted as lag rather than replayed.
- Root and Agent modules must pass with `GOWORK=off` where applicable.

---

### Task 1: Typed domain probes and Agent capabilities

**Files:**
- Modify: `internal/domain/monitor.go`
- Test: `internal/domain/monitor_test.go`
- Modify: `internal/domain/agent.go`
- Test: `internal/domain/agent_test.go`

**Interfaces:**
- Produces: `domain.ProbeDefinition`, `domain.TCPProbe`, `domain.DNSProbe`, `domain.TLSExpectation`
- Produces: `domain.NewTCPMonitor`, `domain.NewDNSMonitor`, and `domain.Monitor.Probe()`
- Produces: `domain.CapabilityTCP` and `domain.CapabilityDNS`
- Consumes: existing `domain.Monitor`, `domain.HTTPProbe`, and threshold invariants

- [ ] **Step 1: Write failing typed-probe tests**

Add literal behavior tests:

```go
func TestNewTCPMonitorNormalizesAndValidatesEndpoint(t *testing.T) {
	monitor, err := domain.NewTCPMonitor(domain.NewTCPMonitorParams{
		ID: "monitor-1", Name: "postgres", Interval: time.Minute,
		Timeout: 5 * time.Second, FailureThreshold: 3, RecoveryThreshold: 2,
		TCP: domain.TCPProbe{
			Host: "db.internal.", Port: 5432,
			Send: []byte("PING\r\n"), Expect: []byte("PONG"),
		},
		CreatedAt: time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC),
	})
	if err != nil { t.Fatal(err) }
	if monitor.Kind != domain.MonitorKindTCP || monitor.TCP.Host != "db.internal" {
		t.Fatalf("monitor = %#v", monitor)
	}
	if monitor.Probe().Kind != domain.MonitorKindTCP {
		t.Fatalf("probe = %#v", monitor.Probe())
	}
}

func TestNewDNSMonitorRejectsUnsupportedRecordType(t *testing.T) {
	_, err := domain.NewDNSMonitor(domain.NewDNSMonitorParams{
		ID: "monitor-1", Name: "dns", Interval: time.Minute,
		Timeout: 5 * time.Second, FailureThreshold: 3, RecoveryThreshold: 2,
		DNS: domain.DNSProbe{Name: "example.com", RecordType: "CAA"},
		CreatedAt: time.Now(),
	})
	if !errors.Is(err, domain.ErrInvalidMonitor) { t.Fatalf("error = %v", err) }
}
```

Extend capability tests to prove `http`, `tcp`, and `dns` are accepted once each and unknown or duplicate values fail.

- [ ] **Step 2: Verify the tests fail for missing types**

Run:

```bash
go test ./internal/domain -run 'TCPMonitor|DNSMonitor|AgentCapabilities' -v
```

Expected: compilation fails because TCP/DNS types and capabilities do not exist.

- [ ] **Step 3: Implement the typed domain union**

Add:

```go
const (
	MonitorKindHTTP MonitorKind = "http"
	MonitorKindTCP  MonitorKind = "tcp"
	MonitorKindDNS  MonitorKind = "dns"
)

type TLSExpectation struct {
	MinimumRemaining time.Duration
}

type TCPProbe struct {
	Host string
	Port uint16
	Send []byte
	Expect []byte
	TLS *TLSExpectation
}

type DNSProbe struct {
	Resolver string
	Name string
	RecordType string // A, AAAA, CNAME, MX, NS, TXT, SRV
	ExpectedValues []string
}

type ProbeDefinition struct {
	Kind MonitorKind
	HTTP HTTPProbe
	TCP TCPProbe
	DNS DNSProbe
}
```

Extend `HTTPProbe` with `Headers map[string]string`, bounded `Body []byte`,
`BodyDoesNotContain []string`, and `TLS *TLSExpectation`. Add TCP/DNS
constructors using the same shared schedule/threshold validation as HTTP.
Normalize host/name values by trimming whitespace and one trailing dot. Reject
ports equal to zero, send/expect bodies over 4 KiB, DNS names over 253 bytes,
record types outside the literal list, and more than 20 expected values.

`Monitor.Probe()` returns the union selected by `Monitor.Kind`.

- [ ] **Step 4: Run domain tests**

Run:

```bash
go test -race ./internal/domain
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain
git commit -m "feat(domain): model TCP and DNS probes"
```

---

### Task 2: Discriminated public probe and work contract

**Files:**
- Modify: `api/openapi.yaml`
- Modify generated: `internal/adapters/httpapi/generated.gen.go`
- Modify generated: `sdk/generated.gen.go`
- Modify generated: `agent/internal/controlplane/generated.gen.go`
- Test: `api/contract_test.go`
- Test: `sdk/helpers_test.go`

**Interfaces:**
- Produces: OpenAPI `ProbeDefinition` union with `kind` discriminator
- Produces: generated `ProbeDefinition` and `ProbeWork` union types
- Preserves temporarily: HTTP-only endpoint fields so handwritten handlers remain buildable until Task 4
- Produces: protocol-neutral nullable result observations and `ProtocolTimings`
- Consumes: Task 1 domain kinds and existing stable operation IDs

- [ ] **Step 1: Write failing contract tests**

Load the generated spec and assert:

```go
func TestProbeDefinitionHasThreeDiscriminatedVariants(t *testing.T) {
	spec, err := httpapi.GetSwagger()
	if err != nil { t.Fatal(err) }
	probe := spec.Components.Schemas["ProbeDefinition"].Value
	if probe.Discriminator == nil || probe.Discriminator.PropertyName != "kind" {
		t.Fatalf("discriminator = %#v", probe.Discriminator)
	}
	if len(probe.OneOf) != 3 {
		t.Fatalf("variants = %d", len(probe.OneOf))
	}
}
```

Add a request-validation test proving a TCP monitor with an HTTP field is
rejected and a valid DNS monitor is accepted by the OpenAPI middleware.

- [ ] **Step 2: Verify red**

Run:

```bash
go test ./api ./internal/adapters/httpapi -run 'ProbeDefinition|TCPMonitorContract|DNSMonitorContract'
```

Expected: FAIL because `ProbeDefinition` is absent.

- [ ] **Step 3: Extend the OpenAPI schema**

Introduce:

```yaml
ProbeDefinition:
  oneOf:
    - {$ref: '#/components/schemas/HTTPProbe'}
    - {$ref: '#/components/schemas/TCPProbe'}
    - {$ref: '#/components/schemas/DNSProbe'}
  discriminator:
    propertyName: kind
    mapping:
      http: '#/components/schemas/HTTPProbe'
      tcp: '#/components/schemas/TCPProbe'
      dns: '#/components/schemas/DNSProbe'
```

Each variant is a closed object with a required `kind` enum containing one
value. HTTP adds bounded headers/body, positive and negative body assertions,
and optional `tlsMinimumRemainingSeconds`. TCP requires host and port and has
base64 `send`, base64 `expect`, and optional TLS threshold. DNS requires name
and record type, with optional resolver and at most 20 expected values.

Add protocol-neutral `ProbeWork` while retaining the HTTP-only work response
reference until Task 5 switches the leasing handler atomically. Keep the
existing required `http` monitor fields deprecated until Task 4 switches
create/get mappings atomically; the final milestone contract contains only
`probe`.

`ProbeResultInput.observedStatus` and
`bodyAssertionPassed` become nullable. Add nullable `observedValues`,
`tlsNotAfter`, and:

```yaml
ProtocolTimings:
  type: object
  additionalProperties: false
  properties:
    dnsMillis: {type: integer, format: int64, minimum: 0}
    connectMillis: {type: integer, format: int64, minimum: 0}
    tlsMillis: {type: integer, format: int64, minimum: 0}
    firstByteMillis: {type: integer, format: int64, minimum: 0}
```

Add stable error codes `dns_mismatch`, `tcp_expect_mismatch`, and
`tls_expiring`.

- [ ] **Step 4: Regenerate and test all consumers**

Run:

```bash
go generate ./internal/adapters/httpapi ./sdk
cd agent && GOWORK=off go generate ./internal/controlplane && cd ..
go test ./api ./sdk ./internal/adapters/httpapi
cd agent && GOWORK=off go test ./internal/controlplane && cd ..
```

Expected: generated clients compile and contract tests pass.

- [ ] **Step 5: Verify compatibility and commit**

Run:

```bash
go tool vacuum lint -d api/openapi.yaml
git diff --check
git add api internal/adapters/httpapi/generated.gen.go sdk agent/internal/controlplane
git commit -m "feat(api): publish TCP and DNS probe contract"
```

---

### Task 3: SQLite typed-probe migration and result observations

**Files:**
- Create: `db/migrations/sqlite/00002_protocol_breadth.sql`
- Modify: `db/queries/sqlite/configuration.sql`
- Modify: `db/queries/sqlite/runs.sql`
- Modify: `db/queries/sqlite/results.sql`
- Modify generated: `db/generated/sqlite/`
- Modify: `internal/application/store.go`
- Modify: `internal/adapters/sqlite/store.go`
- Test: `internal/adapters/sqlite/migrate_test.go`
- Test: `internal/adapters/sqlite/store_test.go`

**Interfaces:**
- Produces: canonical `probe_json` plus `kind` persistence for Monitors and CheckRuns
- Produces: nullable protocol observations and timing JSON on `ProbeResultRecord`
- Consumes: Task 1 `domain.ProbeDefinition`

- [ ] **Step 1: Write failing migration and round-trip tests**

Create a version-1 database containing an HTTP monitor, apply all migrations,
and assert it reads as `MonitorKindHTTP`. Add repository round trips for TCP
and DNS monitors and results with observed DNS values and timing fields. The
test catches a migration that loses the existing HTTP row or allows a monitor
kind/probe mismatch.

- [ ] **Step 2: Verify red**

Run:

```bash
go test ./internal/adapters/sqlite -run 'ProtocolBreadthMigration|TCPMonitorRoundTrip|DNSResultRoundTrip' -v
```

Expected: FAIL because migration 2 and typed repository fields are absent.

- [ ] **Step 3: Implement migration 2**

Use a Goose `NO TRANSACTION` migration. Disable foreign keys on the connection,
create `monitors_v2` with `kind IN ('http','tcp','dns')` and one non-null
`probe_json`, copy `http_json` to `probe_json`, replace the table, and re-enable
foreign keys. Run `PRAGMA foreign_key_check` before completing.

Add `probe_kind TEXT NOT NULL DEFAULT 'http'` to `check_runs`. Add nullable
`observed_values_json`, `tls_not_after`, and `protocol_timings_json` columns to
`probe_results`.

- [ ] **Step 4: Update ports and SQLite mappings**

Change `NewRunRecord.Probe` and `RunRecord.Probe` to
`domain.ProbeDefinition`. Extend `ProbeResultRecord` with:

```go
ObservedValues []string
TLSNotAfter *time.Time
ProtocolTimings ProtocolTimings
```

where `application.ProtocolTimings` contains `time.Duration` fields. Persist
JSON arrays/objects canonically and map absent values to nil/zero. Reject a
stored kind that does not match the decoded union.

- [ ] **Step 5: Generate and verify**

Run:

```bash
go tool sqlc generate
go tool sqlc diff
go test -race ./internal/adapters/sqlite ./internal/application
```

Expected: PASS with clean generation.

- [ ] **Step 6: Commit**

```bash
git add db internal/application/store.go internal/adapters/sqlite
git commit -m "feat(sqlite): persist typed probe definitions"
```

---

### Task 4: TCP and DNS monitor management through the public API

**Files:**
- Modify: `internal/application/configuration.go`
- Test: `internal/application/configuration_test.go`
- Modify: `internal/adapters/httpapi/configuration.go`
- Test: `internal/adapters/httpapi/configuration_test.go`
- Modify: `sdk/helpers.go`
- Test: `sdk/helpers_test.go`

**Interfaces:**
- Produces: `ConfigurationService.CreateMonitor(context.Context, CreateMonitorCommand)`
- Produces: strict create/get mapping for every `ProbeDefinition` variant
- Consumes: Tasks 1-3 typed domain, generated union, and repositories

- [ ] **Step 1: Write failing creation tests**

Create one TCP and one DNS monitor through the strict server against migrated
SQLite. Read each through the SDK and assert the exact kind, host/port or
resolver/name/type/values, thresholds, and assignment. Also assert mismatched
kind/union requests return a validation problem without a database row.

- [ ] **Step 2: Verify red**

Run:

```bash
go test ./internal/application ./internal/adapters/httpapi ./sdk -run 'CreateTCPMonitor|CreateDNSMonitor|ProbeVariantMismatch' -v
```

Expected: FAIL because the generic command and mappings are absent.

- [ ] **Step 3: Generalize the application command**

Replace `CreateHTTPMonitorCommand` with:

```go
type CreateMonitorCommand struct {
	Name string
	LocationID domain.LocationID
	RequiredLocation bool
	Interval time.Duration
	Timeout time.Duration
	FailureThreshold uint16
	RecoveryThreshold uint16
	Probe domain.ProbeDefinition
}
```

Dispatch to the matching domain constructor, then reuse the existing atomic
Monitor/assignment/initial-health transaction.

- [ ] **Step 4: Map the generated union**

Add boundary helpers `probeFromAPI(ProbeDefinition) (domain.ProbeDefinition,
error)` and `probeToAPI(domain.ProbeDefinition) (ProbeDefinition, error)`.
Decode base64 TCP send/expect with a 4 KiB post-decode bound. Sort HTTP header
keys and DNS expected values when producing responses so fixtures remain
stable. Never include secret-valued headers in response models; this milestone
accepts only non-secret literal headers.

In the same schema/generation change, replace the temporary deprecated
`CreateMonitorRequest.http` and `Monitor.http` fields with required `probe`
fields, regenerate root and Agent consumers, and then update these mappings.

- [ ] **Step 5: Test and commit**

Run:

```bash
go test -race ./internal/application ./internal/adapters/httpapi ./sdk
git add internal/application/configuration.go internal/application/configuration_test.go \
  internal/adapters/httpapi/configuration.go internal/adapters/httpapi/configuration_test.go \
  sdk/helpers.go sdk/helpers_test.go
git commit -m "feat(monitors): configure TCP and DNS checks"
```

---

### Task 5: Generic capability-aware leasing and bounded scheduler recovery

**Files:**
- Modify: `internal/application/scheduler.go`
- Test: `internal/application/scheduler_test.go`
- Modify: `internal/application/leasing.go`
- Test: `internal/application/leasing_test.go`
- Modify: `internal/application/store.go`
- Modify: `db/queries/sqlite/runs.sql`
- Modify generated: `db/generated/sqlite/`
- Modify: `internal/adapters/sqlite/store.go`
- Modify: `internal/adapters/httpapi/work.go`
- Test: `internal/adapters/httpapi/work_test.go`

**Interfaces:**
- Produces: `LeaseService.LeaseProbe(context.Context, AgentID, []AgentCapability, time.Duration) (*ProbeWork, error)`
- Produces: one protocol-neutral `ProbeWork`
- Produces: `Scheduler.EnqueueDueWithStats(context.Context, int) (SchedulerStats, error)`
- Preserves: `Scheduler.EnqueueDue(context.Context, int) (int, error)` as a compatibility wrapper returning `stats.Inserted`
- Consumes: typed runs and Agent-advertised capabilities

- [ ] **Step 1: Write failing scheduling and leasing tests**

Prove:

1. an HTTP-only Agent cannot lease a TCP run;
2. an Agent advertising TCP and DNS receives the oldest compatible run;
3. the server rejects lease-request capabilities not present in the
   authenticated Agent record;
4. a Monitor 100 intervals behind creates one run, advances beyond database
   time, and reports 99 skipped intervals rather than replaying 100 runs.

- [ ] **Step 2: Verify red**

Run:

```bash
go test ./internal/application ./internal/adapters/sqlite ./internal/adapters/httpapi \
  -run 'CompatibleProbe|AdvertisedCapabilities|BoundedCatchUp' -v
```

Expected: FAIL against HTTP-only claims and integer-only scheduler result.

- [ ] **Step 3: Implement atomic compatible claims**

Change `ClaimHTTP` to `ClaimProbe(ctx, ClaimRunParams)` where params include
`Capabilities []domain.AgentCapability`. The SQLite query joins the Agent,
checks location, and filters `probe_kind IN (sqlc.slice('capabilities'))`
inside the atomic `UPDATE ... RETURNING` claim. Map capabilities to exact
`http`, `tcp`, and `dns` strings before calling sqlc.

`LeaseProbe` verifies the request is a non-empty subset of the stored Agent
capabilities and retains the 30-second maximum long poll.

- [ ] **Step 4: Implement bounded catch-up accounting**

Add:

```go
type SchedulerStats struct {
	Inserted int
	SkippedIntervals uint64
	MaximumLag time.Duration
}
```

For each due assignment, schedule only the latest interval not after database
time, count earlier missed intervals as skipped, and atomically advance
`next_run_at` to the first future interval. Preserve the unique
Monitor/Location/scheduled-time invariant. Keep all current callers buildable
through the existing `EnqueueDue` wrapper; metrics and later operations code
consume `EnqueueDueWithStats`.

- [ ] **Step 5: Generate, race-test, and commit**

Run:

```bash
go tool sqlc generate
go test -race ./internal/application ./internal/adapters/sqlite ./internal/adapters/httpapi
git add db internal/application internal/adapters/sqlite internal/adapters/httpapi/work.go \
  internal/adapters/httpapi/work_test.go
git commit -m "feat(scheduler): lease compatible protocol work"
```

---

### Task 6: SSRF-safe TCP executor

**Files:**
- Create: `agent/probe/tcp.go`
- Test: `agent/probe/tcp_test.go`
- Modify: `agent/probe/policy.go`
- Test: `agent/probe/policy_test.go`

**Interfaces:**
- Produces: `TCPExecutor.Execute(context.Context, controlplane.ProbeWork) controlplane.ProbeResultInput`
- Consumes: shared `probe.Policy` target resolution and generated TCP work

- [ ] **Step 1: Write failing real-socket tests**

Use a loopback `net.Listener` explicitly allowed by policy. Assert bounded
send/expect succeeds, a mismatched response returns
`tcp_expect_mismatch`, timeout returns `timeout`, and metadata/private targets
without an allow-list return `target_denied`. A TLS test uses a generated
test certificate and asserts its expiration is observed.

- [ ] **Step 2: Verify red**

Run:

```bash
cd agent
GOWORK=off go test ./probe -run TCPExecutor -v
```

Expected: compilation fails because `TCPExecutor` is absent.

- [ ] **Step 3: Implement TCP execution**

Resolve once through `Policy`, dial the validated IP while preserving the
original host as TLS ServerName, apply one context-derived deadline to dial,
write, and read, cap both buffers at 4 KiB, and close the connection after one
probe. Match `expect` as a byte substring. Return only the bounded sanitized
diagnostic sample, stable error code, total/connect/TLS timing, and observed
certificate expiry.

- [ ] **Step 4: Race-test and commit**

Run:

```bash
GOWORK=off go test -race ./probe
GOWORK=off go vet ./probe
cd ..
git add agent/probe
git commit -m "feat(agent): execute TCP probes"
```

---

### Task 7: Resolver-pinned DNS executor

**Files:**
- Create: `agent/probe/dns.go`
- Test: `agent/probe/dns_test.go`
- Modify: `agent/go.mod`
- Modify: `agent/go.sum`

**Interfaces:**
- Produces: `DNSExecutor.Execute(context.Context, controlplane.ProbeWork) controlplane.ProbeResultInput`
- Consumes: shared policy for custom resolver validation and generated DNS work

- [ ] **Step 1: Write failing local-DNS tests**

Run a local UDP/TCP DNS test server that returns literal A, AAAA, CNAME, MX,
NS, TXT, and SRV fixtures. Assert normalization ignores a trailing dot where
DNS semantics permit it, sorts values before comparison, retries a truncated
UDP answer over TCP, reports `dns_mismatch`, and denies a custom resolver not
covered by the private allow-list.

- [ ] **Step 2: Verify red**

Run:

```bash
cd agent
GOWORK=off go test ./probe -run DNSExecutor -v
```

Expected: compilation fails because `DNSExecutor` is absent.

- [ ] **Step 3: Implement DNS execution**

Use `github.com/miekg/dns` v1.1.72, the newest tagged module version returned
by `go list -m -versions` during planning.
Validate and resolve the configured resolver before dialing it; never let the
DNS library perform an unvalidated second lookup. Use UDP first and retry a
truncated response over TCP. Canonicalize record values by type, sort them,
compare the complete expected subset, and persist at most 20 values totaling
4 KiB.

- [ ] **Step 4: Verify module independence and commit**

Run:

```bash
GOWORK=off go mod tidy
GOWORK=off go test -race ./probe
GOWORK=off go vet ./...
cd ..
git add agent
git commit -m "feat(agent): execute DNS probes"
```

---

### Task 8: HTTP TLS assertions and true batched Agent uploads

**Files:**
- Modify: `agent/probe/http.go`
- Test: `agent/probe/http_test.go`
- Modify: `agent/worker/worker.go`
- Test: `agent/worker/worker_test.go`
- Modify: `agent/cmd/xisnove-agent/main.go`
- Test: `agent/cmd/xisnove-agent/main_test.go`

**Interfaces:**
- Produces: protocol dispatcher implementing `Executor.Execute(ProbeWork)`
- Produces: bounded in-memory result queue that uploads batches of 1-100
- Consumes: Tasks 2, 6, and 7 executors

- [ ] **Step 1: Write failing TLS and batching tests**

Add an HTTPS server with a certificate expiring inside the configured
threshold and assert `tls_expiring`, `TLSNotAfter`, DNS/connect/TLS/first-byte
timings, headers, request body, and negative body assertions. In the worker,
queue three completed results, make the first upload return 503, then assert
the retry sends one batch containing all three exactly once and accepts mixed
`accepted`/`duplicate` acknowledgements.

- [ ] **Step 2: Verify red**

Run:

```bash
cd agent
GOWORK=off go test ./probe ./worker -run 'TLSExpiry|ProtocolTimings|BatchUpload' -v
```

Expected: FAIL because these observations and multi-result draining are absent.

- [ ] **Step 3: Implement HTTP breadth and dispatcher**

Record DNS, connect, TLS, and first-byte timings using `httptrace`. Send only
contract-allowed headers and bounded request body. Evaluate every positive and
negative assertion. For TLS, use the verified connection state and compare
leaf `NotAfter` with the Agent clock plus the threshold.

Create a dispatcher that requires exactly one matching work variant and calls
HTTP, TCP, or DNS executor. The command advertises all enabled capabilities;
environment `XISNOVE_AGENT_CAPABILITIES=http,tcp,dns` defaults to all three.

- [ ] **Step 4: Implement bounded batching**

Keep the capacity-100 in-memory channel. After each execution, drain available
results into one slice capped at 100. Retry the identical batch on network/5xx
failures. Remove only IDs acknowledged as `accepted` or `duplicate`; requeue
unacknowledged IDs without exceeding the bound. Do not write queue contents to
disk or logs.

- [ ] **Step 5: Verify and commit**

Run:

```bash
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
cd ..
git add agent
git commit -m "feat(agent): batch multi-protocol observations"
```

---

### Task 9: Stale-location projection and protocol integration

**Files:**
- Create: `internal/application/staleness.go`
- Test: `internal/application/staleness_test.go`
- Create: `db/migrations/sqlite/00003_staleness.sql`
- Modify: `internal/application/store.go`
- Modify: `db/queries/sqlite/health.sql`
- Modify generated: `db/generated/sqlite/`
- Modify: `internal/adapters/sqlite/store.go`
- Modify: `cmd/xisnove-server/serve.go`
- Create: `integration/protocol_breadth_test.go`
- Modify: `README.md`
- Modify: `docs/operations/first-observation.md`
- Modify: `docs/development.md`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Produces: `StalenessService.MarkDue(context.Context, int) (int, error)`
- Produces: end-to-end TCP, DNS, HTTP TLS-expiry, bounded catch-up, and stale Incident proof
- Consumes: all prior protocol-breadth tasks

- [ ] **Step 1: Write failing stale-location tests**

Given last observation `T`, interval one minute, monitor timeout five seconds,
and lease duration 45 seconds, assert the stored stale deadline is
`T + 2m + 50s`. Before the deadline no transition occurs. At or after it, one
transaction changes the location and aggregate to `unknown`, opens or changes
one warning Incident, and appends one event. A second sweep is idempotent.

- [ ] **Step 2: Verify red**

Run:

```bash
go test ./internal/application ./internal/adapters/sqlite -run Staleness -v
```

Expected: FAIL because stale deadlines and sweeps are absent.

- [ ] **Step 3: Implement durable stale deadlines**

Add nullable `stale_at` to `location_health` in migration 3. Every accepted
result sets:

```go
staleAt := finishedAt.Add(2*monitor.Interval + monitor.Timeout + leaseDuration)
```

Pass the configured lease duration into `ResultService`. Claim due health rows
in bounded database-time order. Reuse the same aggregate and Incident
transition function as result ingestion so the stale sweep cannot diverge.
Initial pending rows remain non-notifying.

- [ ] **Step 4: Run stale sweeps in the server**

Run one sweep immediately and every second beside the scheduler. Shutdown
stops both loops before closing the database. Log only IDs and stable error
classes.

- [ ] **Step 5: Write protocol breadth integration tests**

Through the public SDK and generated Agent contract:

1. create HTTP/TLS, TCP, and DNS monitors;
2. enroll an Agent advertising all three capabilities;
3. enqueue and lease one of each kind;
4. execute against local real protocol servers;
5. upload one three-result batch;
6. assert exact observations and all three health projections;
7. simulate database-time scheduler lag and assert bounded catch-up;
8. advance a fake database clock past `stale_at` and assert one warning
   Incident without duplicate events.

- [ ] **Step 6: Update docs and CI**

Document TCP/DNS monitor examples, Agent capability selection, resolver/private
CIDR policy, TLS expiry semantics, scheduler lag, and staleness. Add an
Agent-with-workspace test job in addition to the existing `GOWORK=off` job.

- [ ] **Step 7: Run full verification**

Run:

```bash
make check
go test -race ./integration -run TestProtocolBreadth -count=10
git status --short
```

Expected: deterministic generation, zero OpenAPI lint errors, clean sqlc diff,
root/Agent vet and race tests, ten stable protocol integrations, and only the
intended documentation/CI changes before staging.

- [ ] **Step 8: Commit**

```bash
git add db internal cmd integration README.md docs .github
git commit -m "feat(protocols): complete TCP and DNS observation path"
```

---

## Final milestone 2A verification

Run from the repository root:

```bash
make check
go test -race ./integration -run 'TestFirstObservation|TestProtocolBreadth' -count=10
cd agent && GOWORK=off go test -race ./... && cd ..
git status --short --branch
```

Expected:

- Existing HTTP first-observation behavior remains compatible.
- HTTP/TLS, TCP, and DNS monitors all traverse the public contract, scheduler,
  capability-aware lease, independent Agent executor, batched ingestion,
  health projection, and Incident model.
- Scheduler downtime cannot create an unbounded catch-up storm.
- Required locations become `unknown` only after their durable stale deadline.
- Generation and sqlc output are clean, both modules pass vet/race, and the
  worktree contains one focused commit per task.

After this plan lands, write milestone 2B for PostgreSQL, local Turso, managed
Turso, backup/restore, migrations, and the cross-adapter persistence conformance
suite. Do not mix database-driver work into this protocol plan.
