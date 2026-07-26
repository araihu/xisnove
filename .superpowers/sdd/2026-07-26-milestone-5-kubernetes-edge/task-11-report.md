# Task 11 report — Kubernetes edge kind journey

Date: 2026-07-26
Branch: `codex/milestone-4a-control-plane`
Chart: `xisnove-edge` `0.1.0` (`appVersion: 0.1.0`)

## Frozen topology and contract

The acceptance harness runs a SQLite-backed Xisnove control plane in an
external Docker container on the kind network. Only the operator and discovery
Agent run in the monitored cluster. Helm, `kubectl`, and the generated public
Go SDK drive the journey; neither workload reaches the relational database.

The journey proves Monitor and Agent materialization, a real complete-empty
snapshot, Service/Ingress discovery, explicit candidate promotion, candidate
absence without promoted-Monitor deletion, get/list/watch Secret denial for
the discovery identity, interrupted overlap-safe credential rotation, API
partition recovery, independent operator/Agent/control-plane restarts, and
same-name/new-UID ownership refusal. It also asserts that no Alert, Incident,
result, notification, delivery CRD, or operational Job exists.

Supported discovery watchers are Services, EndpointSlices, Ingresses, and the
optional Gateway API Gateway, HTTPRoute, and GRPCRoute resources. The fixture
uses Service and Ingress because Gateway API CRDs are deliberately optional.
Secret contents and certificate private material are unsupported and forbidden
by RBAC. Promotion remains an explicit API action.

The previous Agent credential stays valid through crashes and partitions and
is revoked only after the control plane records a heartbeat authenticated by
the next generation. The per-candidate in-process promotion gate is only a
contention optimization. Durable link/CAS constraints and retryable
transactions remain the cross-replica boundary; an extra discarded UUID
allocation across replicas is harmless and is not a serialization guarantee.

## Reproducible inputs

- Go `1.26.5`.
- kind `v0.31.0`; node image
  `kindest/node:v1.35.0@sha256:452d707d4862f52530247495d180205e029056831160e22870e37e3f6c1ac31f`.
- Helm `v3.16.4`; kubectl `v1.35.4` with release checksum verification in CI.
- Builder image
  `golang:1.26.5-alpine3.23@sha256:622e56dbc11a8cfe87cafa2331e9a201877271cbff918af53d3be315f3da88cc`.
- Runtime image
  `alpine:3.23.3@sha256:25109184c71bdad752c8312a8623239686a9a2071e8825f20acb8f2198c3f659`.

The final clean `make kind-edge-e2e` run passed `TestKubernetesEdgeKind` in
37.24s (package 37.744s). Its exact cluster, server container, SQLite volume, three
images, temporary credential directory, and artifacts were absent after the
KEEP=0 cleanup. Older failed-run resources were enumerated and removed by
exact name before the clean run.

Failure evidence exports workload resources, cluster logs, and Secret
name/type/key metadata only. A static regression rejects Secret JSON/YAML
serialization, and no artifact, runtime log, token, or `.env` file is committed.
The harness also requires `.dockerignore` exclusions for `.env`, `.env.*`,
`.git`, `.artifacts`, `.superpowers`, and `.worktrees`. This guard was added
after a pre-journey build inspection found that the unfiltered build context
could include local state. The run was stopped, the full recoverable default
builder cache was pruned (5.334GB; verified 0B), and the final clean contexts
were 5.24MB for server, 33.46kB for operator, and 1.80kB for Agent.

## Production defects exposed by kind

1. Controller-runtime health handlers registered paths without a leading slash
   and panicked in `ServeMux`; `/healthz` and `/readyz` plus their regression
   test now use valid paths.
2. The HTTP adapter discarded `complete`/`completedAt`, so a real empty complete
   discovery snapshot became an invalid empty partial; the full public contract
   now reaches the application, complete-empty is accepted, and contract-valid
   partial snapshots retain their completion timestamp.
3. relational Agent reads did not expose the latest non-revoked credential
   generation that had authenticated a heartbeat; shared SQLite/PostgreSQL sqlc
   queries and operator-edge conformance now enforce the heartbeat revocation
   fence.

The managed Turso gate additionally exposed canonical libSQL `database is
locked` text that had lost its structured code. Exact classification now maps
only that canonical form to `ErrRetryableTransaction`; near matches remain
non-retryable. Promotion retries rolled-back transactions, and same-process
same-candidate contention uses a bounded, context-cancelable gate with cleanup
tests. The concurrent assertion was not weakened.

## Verification evidence

- `make kind-edge-e2e`: PASS; 37.24s test, 37.744s package; zero residue.
- Managed Turso `TestOperatorEdgeStorage/TursoCloud`: PASS; all 9 subjourneys,
  96.01s test, 97.662s package. Platform API confirmed zero remaining
  `xisnove-ci-*` databases after exact deletion/404 polling.
- `make check`: PASS; generated/sqlc drift clean, root/Agent/CLI/UI checks pass;
  Vacuum reports 309 pre-existing warnings and 0 errors.
- `make -C operator verify`, `make -C operator envtest`, standalone operator
  race tests and vet: PASS; envtest 8.823s.
- `helm lint charts/xisnove-edge`: PASS, 1 chart and 0 failures.
- `go test -race ./integration -run 'StorageMatrix|OperatorEdge' -count=1`:
  PASS; 47.647s with SQLite, local Turso, and PostgreSQL (managed Turso is the
  separate protected gate above).
- Context cancellation/map cleanup and focused promotion/adapter race tests:
  PASS for 10 repetitions.

No deferred Task 11 correctness concern remains. Automatic scheduled
credential rotation, cluster-wide promotion serialization, operational
Kubernetes persistence, and mandatory Gateway API installation remain outside
the approved v1 scope.
