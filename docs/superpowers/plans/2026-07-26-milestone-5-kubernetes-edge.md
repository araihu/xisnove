# Milestone 5 Kubernetes Edge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:executing-plans` to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking. Every production behavior follows
> red-green-refactor, and a task is not integrated until its listed gates pass.

**Goal:** Deliver a production-capable Kubernetes edge operator and discovery
agent that reconcile Monitor and Agent CRDs through the public API, provision
crash-safe hash-only credentials, report bounded status, and discover eligible
cluster endpoints without granting discovery workloads Secret access.

**Architecture:** The relational control plane remains authoritative. Kubernetes
CRDs are desired-state inputs and bounded observations, never an alternative
operational database. Controllers use only the generated public Go SDK through a
narrow adapter. Operator ownership is persisted provider-neutrally by stable
owner key plus Kubernetes UID. Agent credentials are generated locally by the
operator, pre-staged in an unmounted Secret key, atomically registered as hashes
through the API, then promoted to one coherent mounted credential bundle. The
agent watches Kubernetes resources with read-only RBAC and publishes complete
or partial discovery snapshots; only successful complete snapshots mark missing
candidates absent.

**Tech Stack:** Go 1.26.5, OpenAPI 3.1.2, oapi-codegen v2.8.0, sqlc v1.31.1,
controller-runtime, Kubernetes CRDs/RBAC/Leases, Helm 3, Testcontainers for Go,
envtest, kind, SQLite, local Turso/libSQL, managed Turso, and PostgreSQL.

## Global constraints

- `api/openapi.yaml` is canonical. Generate and commit the root server, public
  SDK, Agent client, and mock artifacts; never edit generated Go manually.
- Core domain, application, and storage packages contain no Kubernetes types.
  Kubernetes UID ownership crosses the boundary as `port.ExternalOwner`.
- The control plane stores only credential hashes. Plaintext is supplied by the
  operator, never returned, persisted, logged, placed in status, or placed in
  command arguments.
- All six operator mutations require `Idempotency-Key`, use the
  `operator:provision` scope, and use stable action endpoints where owner keys
  contain `/` or deletion must recover from a lost Kubernetes status write.
- Apply is atomic: owner binding, resource mutation, credential hash, and
  idempotency result commit in one relational transaction. Replaying the same
  owner UID, generation, and hash succeeds; any mismatch returns RFC 9457 409.
- Agent Secret state is `next -> current -> previous`. Only a single JSON
  `current` bundle containing credential and generation is mounted. `next` is
  pre-staged before the API call and is never mounted.
- At most two Agent credentials remain active. Revocation of the previous
  generation is blocked until a heartbeat proves the replacement generation.
- A complete discovery snapshot may be empty. Only a committed complete
  snapshot marks older unlisted candidates `present=false`; partial/failed
  collection never makes absence claims and never clears promotion links.
- Gateway HTTP and HTTPS listeners normalize to the supported HTTP probe.
  GRPCRoute is observed only for diagnostics and is excluded from promotable v1
  candidates until a gRPC probe exists.
- Monitor health includes `pending`. Ready/Synced report reconciliation only;
  Degraded reports health. Conditions are capped at 8 and messages at 256 bytes.
- The discovery ServiceAccount has get/list/watch for Services, EndpointSlices,
  Ingresses, Gateways, HTTPRoutes, and GRPCRoutes, and no Secret permission.
  The operator uses an uncached APIReader for exact Secret reads.
- Leader election uses Leases in the release namespace with get/list/watch/
  create/update/patch. The chart contains no cluster-wide write beyond CRDs and
  status/finalizers required for Xisnove resources.
- Alerts, Incidents, probe results, notification delivery Jobs/CRDs, automatic
  credential rotation, automatic candidate promotion, direct Vault/OpenBao/ESO
  APIs, gRPC probing, and control-plane deployment packaging are out of scope.
- Every relational behavior runs through the shared conformance journey on
  SQLite, local Turso, PostgreSQL Testcontainers, and the protected managed
  Turso harness. Tests never create or delete databases in the protected
  `konclave-ci` group.

## Dependency and ownership DAG

Task 1 is the exclusive contract freeze and lands first. After it is pushed,
Tasks 2, 5, and 6 may run in parallel with exclusive ownership of relational
storage, the operator SDK adapter, and Agent credentials respectively. Task 3
depends on Task 2, and Task 4 depends on Tasks 1 and 3. Task 7 depends on Task 6
and owns Agent Kubernetes discovery. Task 8 joins Tasks 4, 5, and 6 before
changing controllers. Task 9 joins Tasks 7 and 8, so the chart/runtime cannot
freeze without the real discovery producer. Tasks 10 and 11 then run
sequentially because the manager, chart, envtest, and kind journey share runtime
composition. The parent control plane owns integration commits, generated
artifact reconciliation, `go.work`, root `Makefile`, and shared CI changes.

---

### Task 1: Freeze operator and complete-snapshot API contracts

**Files:**
- Modify: `api/openapi.yaml`
- Modify: `api/contract_test.go`
- Create: `api/milestone5_contract_test.go`
- Generate: `internal/adapters/httpapi/generated.gen.go`
- Generate: `sdk/generated.gen.go`
- Generate: `agent/internal/controlplane/generated.gen.go`
- Generate: `internal/mockapi/generated.gen.go`
- Modify: `internal/mockapi/server.go`
- Modify: `internal/mockapi/server_test.go`
- Modify: `internal/adapters/httpapi/authorization.go`
- Modify: `internal/adapters/httpapi/authorization_test.go`
- Modify: `application/port/human_clients.go`
- Modify: `application/api_tokens.go`
- Modify: `application/api_tokens_test.go`
- Modify: `application/ports_compat.go`
- Modify: `docs/contracts/milestone-4-api-and-mock.md`

**Interfaces:**
- Adds scope: `operator:provision`
- Adds operations: `applyOperatorMonitor`, `deleteOperatorMonitor`,
  `applyOperatorAgent`, `putOperatorAgentCredential`,
  `revokeOperatorAgentCredential`, `deleteOperatorAgent`
- Extends discovery input with required `complete` and `completedAt`
- Marks credential fields `writeOnly: true`; no response schema contains them
- Requires `applyOperatorAgent.initialCredential` with generation and plaintext
  for first materialization; later rotations alone use credential PUT

- [ ] **Step 1: Write failing schema and authorization tests**

Assert operation IDs, idempotency headers on all six mutations including delete
and revoke,
the exact scope map, RFC 9457 conflicts, owner `{key, uid}` requirements,
optional deletion `externalId`, required initial Agent credential material,
required complete-snapshot fields, and absence of credential material in
response schemas.

- [ ] **Step 2: Define the public request/response schemas**

Use action endpoints `/v1/operator/monitors:apply`,
`/v1/operator/monitors:delete`, `/v1/operator/agents:apply`, and
`/v1/operator/agents:delete`. Use resource endpoints for credential generation:
`PUT /v1/operator/agents/{agentId}/credentials/{generation}` and
`POST .../{generation}:revoke`. Apply responses include external ID plus bounded
health/observation metadata only.

- [ ] **Step 3: Regenerate every consumer and update the mock**

Run the repository generators. Teach the mock deterministic success, replay,
ownership-conflict, hash-conflict, and empty-complete-snapshot behavior so the
operator adapter can be developed before real handlers land.

- [ ] **Step 4: Verify generated drift and contract isolation**

```bash
make generate
go test -race ./api ./internal/mockapi ./internal/adapters/httpapi ./application
make generated-check
git diff --check
```

- [ ] **Step 5: Commit the frozen contract**

```bash
git add api sdk agent/internal/controlplane internal/mockapi internal/adapters/httpapi application docs/contracts
git commit -m "feat(api): define Kubernetes operator contract"
```

---

### Task 2: Persist external ownership and complete discovery observations

**Files:**
- Create: `db/migrations/sqlite/00010_operator_edge.sql`
- Create: `db/migrations/postgres/00010_operator_edge.sql`
- Modify: `db/migrations/sqlite/migrations.go`
- Modify: `db/migrations/postgres/migrations.go`
- Create: `db/queries/sqlite/operator.sql`
- Create: `db/queries/postgres/operator.sql`
- Modify: `db/queries/sqlite/discovery.sql`
- Modify: `db/queries/postgres/discovery.sql`
- Modify: `db/queries/sqlite/agents.sql`
- Modify: `db/queries/postgres/agents.sql`
- Generate: `db/generated/sqlite/*.go`
- Generate: `db/generated/postgres/*.go`
- Modify: `application/port/discovery.go`
- Create: `application/port/operator.go`
- Create: `contracttest/operator.go`
- Modify: `contracttest/discovery.go`
- Modify: `integration/storage_matrix_test.go`
- Modify: `integration/migration_upgrade_test.go`
- Create: `integration/operator_edge_storage_test.go`
- Modify: `.github/workflows/turso-conformance.yml`

**Interfaces and schema:**

```go
type ExternalOwner struct { Key, UID string }
type OperatorBinding struct {
    Owner ExternalOwner
    Kind string
    ResourceID string
    DeletedAt *time.Time
}

type OperatorRepository interface {
    Resolve(context.Context, ExternalOwner, string) (OperatorBinding, error)
    Bind(context.Context, OperatorBinding) error
    Tombstone(context.Context, ExternalOwner, string, time.Time) error
}
```

Add `operator_resources(owner_key, owner_uid, kind, resource_id, deleted_at)`
with a unique `(kind, resource_id)` constraint. Add complete/completed-at fields
to discovery batches and heartbeat credential generation plus last complete
discovery time to Agent observation queries.

- [ ] **Step 1: Write failing cross-profile conformance journeys**

Cover same-owner replay, a new UID receiving a new remote object, a new UID being
unable to claim the old external ID, resource-ID takeover conflict, owner-only
delete recovery, tombstone replay, empty complete snapshot, rejected empty
partial snapshot, out-of-order complete snapshot, changed timestamp/completeness
replay conflict, stale missing candidates, preserved promotion links, and last
complete sync calculation. Execute the same suite for SQLite, local Turso, and
PostgreSQL; extend `.github/workflows/turso-conformance.yml` with the exact
`OperatorEdge` regex without destructive group operations.

- [ ] **Step 2: Add equivalent migrations and upgrade coverage**

Upgrade a version-9 fixture containing Agents, discovery candidates, and a
promoted Monitor. Prove all data survives with unknown observation defaults.

- [ ] **Step 3: Generate queries and implement transaction-scoped ports**

Complete snapshot application and absent marking occur in the same transaction.
Candidate `last_observed_at` in a complete batch equals `completedAt`; reject
mixed timestamps before storage. Keep server commit time distinct from the
client snapshot observation time and compute freshness from the latest
successfully committed complete batch. `promoted_monitor_id` is never cleared.

- [ ] **Step 4: Run the storage matrix**

```bash
go tool sqlc generate
go tool sqlc diff
go test -race ./contracttest ./internal/adapters/sqlite ./internal/adapters/tursolocal ./internal/adapters/postgres
DOCKER_HOST=unix:///Users/guilhermecastro/.colima/default/docker.sock TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock go test -race ./integration -run 'OperatorEdge|StorageMatrix|Migration'
```

- [ ] **Step 5: Commit the persistence slice**

```bash
git add db application/port contracttest integration internal/adapters .github/workflows/turso-conformance.yml
git commit -m "feat(storage): persist operator ownership and snapshots"
```

---

### Task 3: Implement the provider-neutral operator application service

**Files:**
- Create: `application/operator.go`
- Create: `application/operator_test.go`
- Modify: `application/agents.go`
- Modify: `application/discovery.go`
- Modify: `application/port/operator.go`
- Modify: `application/port/store.go`
- Modify: `application/ports_compat.go`

**Interfaces:**

```go
type CredentialHasher interface { Hash(string) []byte }
type OperatorService struct { Store port.UnitOfWork; Credentials CredentialHasher }
func (s OperatorService) ApplyMonitor(context.Context, ApplyOperatorMonitor) (OperatorMonitorState, error)
func (s OperatorService) DeleteMonitor(context.Context, DeleteOperatorMonitor) error
func (s OperatorService) ApplyAgent(context.Context, ApplyOperatorAgent) (OperatorAgentState, error)
func (s OperatorService) PutAgentCredential(context.Context, PutOperatorCredential) error
func (s OperatorService) RevokeAgentCredential(context.Context, RevokeOperatorCredential) error
func (s OperatorService) DeleteAgent(context.Context, DeleteOperatorAgent) error
```

- [ ] **Step 1: Write failing transaction and replay tests**

Use a recording UnitOfWork to prove binding and mutation share one transaction,
the request fingerprint includes owner UID/generation/credential hash, identical
lost-response retries succeed, mismatches conflict, plaintext cannot be read
from results, and revoke-before-replacement-heartbeat is rejected.

- [ ] **Step 2: Implement monitor apply/delete**

Validate the owner before trusting external ID. Owner-only delete resolves the
binding; exact external ID is consistency proof. Already-absent deletion returns
success and tombstones the binding without synthesizing an Incident.

- [ ] **Step 3: Implement Agent apply/credential/delete**

Hash caller-supplied credentials inside the transaction. Generated DTO strings
cannot be reliably zeroed, so require bounded request bodies, `writeOnly`
schemas, no logging/persistence, best-effort clearing of mutable byte buffers,
and metadata-only responses. Inject the existing `TokenIssuer` as the
`CredentialHasher` implementation. Enforce next-generation sequencing and the
two-active-credential limit. Initial Agent apply includes generation 1 and its
credential in the same transaction as resource creation, owner binding, and the
idempotency result. Credential PUT is exclusively for later generations.

- [ ] **Step 4: Implement complete snapshot semantics**

Reject incomplete timestamps and permit zero candidates. Persist successful
complete observation only after the candidate transaction commits.

- [ ] **Step 5: Verify and commit**

```bash
go test -race ./application -run 'Operator|Credential|Discovery'
git add application
git commit -m "feat(application): reconcile operator resources"
```

---

### Task 4: Expose real operator HTTP handlers

**Files:**
- Create: `internal/adapters/httpapi/operator.go`
- Create: `internal/adapters/httpapi/operator_test.go`
- Modify: `internal/adapters/httpapi/server.go`
- Modify: `cmd/xisnove-server/runtime.go`
- Modify: `cmd/xisnove-server/serve.go`
- Create: `integration/operator_api_test.go`

- [ ] **Step 1: Write failing handler and lost-status tests**

Cover scope denial, malformed owner, exact success bodies, no plaintext response,
same-key replay after a simulated dropped response, different-hash conflict,
a recreated UID receiving a new resource when external ID is absent, conflict
when that UID presents the old external ID, owner-only deletion, and the
replacement-heartbeat revoke guard. Assert bounded RFC 9457 detail/instance and
no secret leakage. A force-deleted CR may leave an orphan; it must never permit
the recreated CR to take ownership of that orphan.

- [ ] **Step 2: Map generated requests to OperatorService**

Handlers contain transport validation and problem mapping only. Preserve request
context and idempotency key. Do not import SQL or Kubernetes packages.

- [ ] **Step 3: Wire the service into the server composition root**

Use the existing UnitOfWork, credential hasher, clock, and authorization map.
Keep admin and Agent scopes isolated from `operator:provision`.

- [ ] **Step 4: Verify and commit**

```bash
go test -race ./internal/adapters/httpapi ./cmd/xisnove-server ./integration -run 'Operator|Ownership|CredentialReplay'
git add internal/adapters/httpapi cmd/xisnove-server integration
git commit -m "feat(http): expose operator reconciliation API"
```

---

### Task 5: Replace the fake operator client with the generated SDK adapter

**Files:**
- Modify: `operator/internal/controlplane/client.go`
- Create: `operator/internal/controlplane/sdk/client.go`
- Create: `operator/internal/controlplane/sdk/client_test.go`
- Modify: `operator/go.mod`
- Modify: `operator/go.sum`

- [ ] **Step 1: Refine the narrow controller boundary**

Replace controller-generated plaintext requests with caller-supplied credential
bytes and distinct owner key/UID. Remove `IssueAgentCredential`; represent put
and revoke explicitly. The interface remains free of generated SDK types.
Update the operator module toolchain declaration to Go 1.26.5 to match the
workspace, root, Agent, CLI, and this plan.

- [ ] **Step 2: Write failing SDK adapter tests against the real mock**

Prove bearer provisioning auth, idempotency headers, exact owner mapping,
successful empty IDs where allowed, 404 mapping, 409 ownership/hash mapping,
non-JSON failure handling, response body closure, and secret redaction.

- [ ] **Step 3: Implement only with the public SDK**

Construct the generated client with its documented bearer and idempotency
helpers. Do not call internal server packages or duplicate HTTP DTOs.

- [ ] **Step 4: Verify and commit**

```bash
GOWORK=off go -C operator test -race ./internal/controlplane/...
GOWORK=off go -C operator vet ./internal/controlplane/...
git add operator/internal/controlplane operator/go.mod operator/go.sum
git commit -m "feat(operator): adapt the public control-plane SDK"
```

---

### Task 6: Make Agent credentials reload-safe and generation-coherent

**Files:**
- Create: `agent/credentials/bundle.go`
- Create: `agent/credentials/bundle_test.go`
- Modify: `agent/worker/worker.go`
- Modify: `agent/worker/worker_test.go`
- Modify: `agent/discovery/publisher.go`
- Modify: `agent/discovery/publisher_test.go`
- Modify: `agent/cmd/xisnove-agent/main.go`
- Modify: `agent/cmd/xisnove-agent/main_test.go`

**Interface:**

```go
type Bundle struct { Credential string `json:"credential"`; Generation int64 `json:"generation"` }
type Provider interface { Current(context.Context) (Bundle, error) }
```

- [ ] **Step 1: Write failing atomic-reload tests**

Replace the mounted file between heartbeat cycles and prove the next request
uses the new credential and matching generation together. Cover partial writes,
invalid JSON, missing fields, permission errors, cancellation, and no secret in
errors/logs. Remove every hardcoded `CredentialGeneration: 1` assertion.

- [ ] **Step 2: Implement a file-backed provider**

Read one bounded JSON file per publish/heartbeat operation, validate before use,
and return a copied immutable bundle. The file path comes from configuration;
credential values never enter flags or environment variables.

- [ ] **Step 3: Inject the provider into Worker and discovery Publisher**

Both acquire the bundle immediately before an authenticated request so Secret
projected-volume updates do not require a pod restart.

- [ ] **Step 4: Verify and commit**

```bash
GOWORK=off go -C agent test -race ./...
GOWORK=off go -C agent vet ./...
git add agent
git commit -m "feat(agent): reload coherent credential bundles"
```

---

### Task 7: Implement Kubernetes discovery in the Agent module

**Files:**
- Create: `agent/discovery/kubernetes/source.go`
- Create: `agent/discovery/kubernetes/source_test.go`
- Create: `agent/discovery/kubernetes/normalize.go`
- Create: `agent/discovery/kubernetes/normalize_test.go`
- Create: `agent/discovery/kubernetes/watcher.go`
- Create: `agent/discovery/kubernetes/watcher_test.go`
- Modify: `agent/discovery/publisher.go`
- Modify: `agent/cmd/xisnove-agent/main.go`
- Modify: `agent/go.mod`
- Modify: `agent/go.sum`
- Delete: `operator/internal/discovery/normalize.go`
- Delete: `operator/internal/discovery/normalize_test.go`

- [ ] **Step 1: Port normalization tests to the owning module**

Cover Services/EndpointSlices, Ingresses, Gateway HTTP/HTTPS listeners, and
HTTPRoutes with deterministic source identity and location. Assert GRPCRoute
produces a bounded unsupported diagnostic and no promotable candidate.

- [ ] **Step 2: Write failing list/watch completion tests**

A successful initial list publishes a complete snapshot, including empty lists.
Watch updates may publish partial snapshots. A relist publishes the next complete
snapshot. Forbidden, timeout, expired resource version, and cancellation never
claim completeness. If the normalized inventory exceeds the API's 500-candidate
batch limit, publish bounded partial batches plus an explicit diagnostic and no
absence claims; v1 does not silently truncate or mark individual chunks
complete.

- [ ] **Step 3: Implement informer-backed collection with bounded queues**

Use client-go dynamic/typed clients behind small testable interfaces. Coalesce
events, bound memory, and shut down on context cancellation. The publisher sets
one batch-level `completedAt`, including for an empty complete snapshot.
Candidate observation timestamps remain distinct where the contract exposes
them.

- [ ] **Step 4: Wire optional Kubernetes discovery into the Agent binary**

Raw/Docker agents run normally without in-cluster config. Kubernetes discovery
activates only when configured and uses the existing generated Agent API client.
Pin client-go, Kubernetes APIs, and Gateway API dependencies in `agent/go.mod`,
then run `GOWORK=off go -C agent mod tidy -diff` before tests.

- [ ] **Step 5: Verify and commit**

```bash
GOWORK=off go -C agent test -race ./discovery/... ./cmd/xisnove-agent
GOWORK=off go -C agent vet ./...
git add agent operator/internal/discovery
git commit -m "feat(agent): discover Kubernetes probe targets"
```

---

### Task 8: Complete controller Secret lifecycle and bounded status

**Files:**
- Modify: `operator/api/v1alpha1/monitor_types.go`
- Modify: `operator/api/v1alpha1/agent_types.go`
- Generate: `operator/api/v1alpha1/zz_generated.deepcopy.go`
- Modify: `operator/internal/controller/common.go`
- Modify: `operator/internal/controller/monitor_controller.go`
- Modify: `operator/internal/controller/monitor_controller_test.go`
- Modify: `operator/internal/controller/agent_controller.go`
- Modify: `operator/internal/controller/agent_controller_test.go`

- [ ] **Step 1: Write failing condition truth-table tests**

Monitor pending/unknown maps to Degraded=Unknown, up to False, down/degraded to
True. Ready/Synced reflect reconciliation only. Agent Workload observes actual
Deployment generation and available replicas. Heartbeat stale and complete
discovery freshness use configurable thresholds. Enforce 8 conditions and
256-byte messages.

- [ ] **Step 2: Write crash-point Secret lifecycle tests**

Exercise crashes before Secret pre-stage, after `next`, after successful API
apply, after promotion, after replacement heartbeat, and after revoke. Reconcile
must converge without generating a different token, exposing `next`, or losing
the prior working credential.

- [ ] **Step 3: Implement local credential generation and promotion**

Generate cryptographically random bytes, store them only in the operator-owned
Secret, and supply them to the SDK adapter. Promote `next` to the mounted current
JSON bundle only after API success; preserve previous until heartbeat confirms
the new generation and revoke succeeds.

- [ ] **Step 4: Preserve owner/finalizer deletion semantics**

Owner key is stable namespace/name/kind; UID is separate and must match. Deletion
uses owner-only recovery when status external ID is absent. Remove finalizer only
after idempotent remote deletion.

- [ ] **Step 5: Generate, verify, and commit**

```bash
make -C operator generate
GOWORK=off go -C operator test -race ./internal/controller ./api/...
make -C operator verify
git add operator/api operator/internal/controller
git commit -m "feat(operator): reconcile credentials and observations"
```

---

### Task 9: Add the operator manager and least-privilege Helm runtime

**Files:**
- Create: `operator/cmd/xisnove-operator/main.go`
- Create: `operator/cmd/xisnove-operator/main_test.go`
- Modify: `charts/xisnove-edge/values.yaml`
- Modify: `charts/xisnove-edge/templates/operator-deployment.yaml`
- Modify: `charts/xisnove-edge/templates/operator-rbac.yaml`
- Modify: `charts/xisnove-edge/templates/agent.yaml`
- Modify: `charts/xisnove-edge/templates/discovery-rbac.yaml`
- Modify: `operator/internal/manifest/chart_test.go`
- Modify: `operator/README.md`
- Modify: `operator/INTEGRATION.md`

- [ ] **Step 1: Write failing manager and rendered-manifest tests**

Assert scheme/controller registration, health/readiness endpoints, graceful
shutdown, configurable heartbeat/discovery thresholds, uncached Secret reader,
leader election, namespaced Lease verbs, credential bundle mount, discovery
read-only resources, and explicit no-Secret discovery RBAC.

- [ ] **Step 2: Implement the controller-runtime manager**

Build the generated SDK adapter from control-plane URL and a file-mounted
provisioning credential. Register Monitor and Agent reconcilers, pass APIReader,
enable Leases by default, and expose probes without leaking configuration.

- [ ] **Step 3: Update the chart and operational docs**

Support `existingSecret` only for the operator's control-plane provisioning
credential. Agent credential Secrets remain operator-owned and mutable; the
controller must refuse adoption. Document the future Vault/OpenBao/ESO seam as
materialization of the operator provisioning Secret only, without direct
provider-specific APIs.

- [ ] **Step 4: Verify and commit**

```bash
GOWORK=off go -C operator test -race ./...
GOWORK=off go -C operator vet ./...
helm lint charts/xisnove-edge
git add operator charts/xisnove-edge
git commit -m "feat(operator): run Kubernetes edge manager"
```

---

### Task 10: Add envtest controller integration coverage

**Files:**
- Create: `operator/internal/controller/envtest_test.go`
- Create: `operator/internal/controller/testdata/fake_control_plane.go`
- Modify: `operator/Makefile`
- Modify: `operator/go.mod`
- Modify: `operator/go.sum`
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Add the envtest harness and failing journeys**

Install CRDs, start manager with leader election disabled for the test, and use
a stateful fake control plane. Cover Monitor create/update/delete, Agent
materialization, owner conflict, crash replay, manual rotation, Deployment
availability, stale heartbeat, complete discovery status, and finalizers.

- [ ] **Step 2: Verify status subresource conflict retries**

Race spec/status updates and prove bounded retry converges without overwriting a
newer generation or exceeding condition/message caps.

- [ ] **Step 3: Wire deterministic tooling and CI**

Add the pinned tool with
`GOWORK=off go -C operator get -tool sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.24.1`.
Have the Makefile resolve Kubernetes 1.36 assets with
`go tool setup-envtest use 1.36.x -p path`, cache the downloaded assets, and
expose `make -C operator envtest`. Do not download unpinned shell executables.

- [ ] **Step 4: Verify and commit**

```bash
make -C operator envtest
make -C operator verify
git add operator .github/workflows/ci.yml
git commit -m "test(operator): exercise reconciliation with envtest"
```

---

### Task 11: Prove the Kubernetes edge journey in kind and freeze Milestone 5

**Files:**
- Create: `integration/kubernetes_edge_kind_test.go`
- Create: `integration/testdata/kind/cluster.yaml`
- Create: `integration/testdata/kind/fixtures.yaml`
- Create: `scripts/kind-edge-e2e.sh`
- Modify: `Makefile`
- Modify: `.github/workflows/ci.yml`
- Modify: `docs/operator/operations.md`
- Modify: `docs/operator/architecture.md`
- Modify: `docs/operations/persistence-conformance.md`
- Modify: `docs/superpowers/specs/2026-07-24-xisnove-v1-design.md`

- [ ] **Step 1: Write the black-box journey**

Start a disposable kind cluster and relational control plane. Install the chart,
verify CRDs/RBAC, create Monitor and Agent resources, observe remote
materialization, publish discovery including an empty complete snapshot, and
promote a candidate through the public API. Assert the discovery ServiceAccount
cannot get/list/watch Secrets.

- [ ] **Step 2: Exercise rotation and failure recovery**

Rotate manually, interrupt the operator between `next`, API apply, and Secret
promotion, restart it, then prove convergence. Partition the API, mutate CRDs,
restore connectivity, and prove owner-safe replay. Delete/recreate a same-name
CR with a new UID and prove it cannot take over the old remote resource.

- [ ] **Step 3: Exercise persistence and process survival**

Restart operator, agent, and control plane independently. Prove promoted
candidates remain linked, previous credentials are revoked only after a new
generation heartbeat, and no Alert/Incident/result/notification CRD or Job was
created.

- [ ] **Step 4: Run every Milestone 5 gate**

```bash
make check
make -C operator verify
make -C operator test
make -C operator envtest
make kind-edge-e2e
DOCKER_HOST=unix:///Users/guilhermecastro/.colima/default/docker.sock TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock go test -race ./integration -run 'StorageMatrix|OperatorEdge'
git diff --check
git status --short
```

- [ ] **Step 5: Document evidence and commit the frozen milestone**

Record exact images, chart version, test durations, supported/unsupported
discovery kinds, credential recovery invariants, and managed Turso evidence.

```bash
git add integration scripts Makefile .github/workflows docs
git commit -m "test(kubernetes): prove the edge reconciliation journey"
git push origin codex/milestone-4a-control-plane
```

Milestone 5 is complete only when the branch is clean and pushed, all relational
profiles share the same ownership/snapshot contract, the operator and agent pass
race tests, envtest and kind pass, and the discovery identity has demonstrably
no Secret access.
