# Open Core Extension Surface Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:executing-plans` to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking. Every behavior change follows
> red-green-refactor.

**Goal:** Make the complete single-tenant Xisnove core safely importable by a
future external composition root without creating or leaking SaaS concepts.

**Architecture:** The canonical module stays `github.com/araihu/xisnove`.
Pure entities and transitions live in `domain`; public use cases live in
`application`; infrastructure contracts and coarse transaction semantics live
in `application/port`; reusable adapter verification lives in `contracttest`.
Self-hosted SQL, HTTP, crypto, and process adapters remain internal. Dependency
inversion, caller-context propagation, external-module compilation, and
transactional atomicity are executable constraints.

**Tech Stack:** Go 1.26.1, standard-library package analysis, existing
SQLite/PostgreSQL/Turso adapters, and the repository's `make check` pipeline.

## Constraints

- Do not create `xisnove-cloud`, tenant IDs, organizations, subscriptions,
  billing, entitlements, plugins, or edition build tags.
- Keep the self-hosted application fully functional; this is a package-boundary
  refactor, not a feature split.
- Generated API and sqlc types remain adapter-boundary details.
- The operational UnitOfWork must atomically cover result ingestion, health,
  Incidents, IncidentEvents, audit, and outbox writes.
- A future ClickHouse adapter belongs behind a distinct analytics/archive port;
  it cannot implement or replace the operational UnitOfWork.
- Do not claim Apache 2.0 licensing until the standard license and required
  notices are committed and verified.

---

### Task 1: Freeze the public boundary with failing architecture tests

**Files:**
- Create: `internal/architecture/dependencies_test.go`
- Create: `internal/architecture/context_test.go`

- [x] Assert `go list -deps` for `domain` contains no application or internal
  adapter package.
- [x] Assert `application` depends on `domain` and `application/port`, never
  `internal/adapters`, generated API types, sqlc types, or database drivers.
- [x] Assert operational and analytical interfaces are distinct declarations.
- [x] Parse non-test application source and reject replacing an incoming
  context with `context.Background()` or `context.TODO()`.
- [ ] Run the tests red while packages still have internal paths.

---

### Task 2: Promote the pure domain package

**Files:**
- Move: `internal/domain/*.go` to `domain/*.go`
- Modify: imports throughout the root module

- [x] Move production and test files without changing domain behavior.
- [x] Rewrite imports to `github.com/araihu/xisnove/domain`.
- [x] Confirm `domain` imports only standard-library packages.
- [x] Run `go test -race ./domain` and relevant adapter tests.

---

### Task 3: Extract public infrastructure ports and UnitOfWork

**Files:**
- Create: `application/port/store.go`
- Create: `application/port/notification.go`
- Create: `application/port/store_test.go`
- Modify: `internal/application/store.go`
- Modify: `internal/application/notification_store.go`

- [x] Move repository interfaces, records, `ErrNotFound`, and the repository set
  to package `port`; records may depend on public `domain` only.
- [x] Introduce the coarse public contract:

```go
type UnitOfWork interface {
    View(context.Context, func(context.Context, Repositories) error) error
    Transact(context.Context, func(context.Context, Repositories) error) error
}
```

- [x] Add contract tests proving callbacks receive the caller's exact context,
  reads use `View`, and failed transactions roll back atomically.
- [x] Do not expose database/sql, pgx, libSQL, or sqlc-generated types.

---

### Task 4: Promote application services and propagate contexts

**Files:**
- Move: `internal/application/*.go` to `application/*.go`
- Modify: every service constructor and method
- Modify: self-hosted composition roots and HTTP adapters

- [x] Update services to consume `port.UnitOfWork` and `port` records.
- [x] Replace `Repositories()` access with `View(ctx, callback)` and
  `WithinTx` with `Transact(ctx, callback)`.
- [x] Thread the callback context into every repository call; never close over
  and substitute another context.
- [x] Preserve constructor behavior and public errors deliberately; document
  exported identifiers needed by external consumers.
- [x] Run `go test -race ./application ./internal/adapters/...`.

---

### Task 5: Adapt all self-hosted operational stores

**Files:**
- Modify: `internal/adapters/sqlitecompat/store.go`
- Modify: `internal/adapters/postgres/store.go`
- Modify: SQLite, local Turso, managed Turso, and database composition adapters

- [x] Implement `View` and `Transact` with the supplied context.
- [x] Ensure transaction-scoped repositories cannot escape or use a closed
  transaction.
- [x] Preserve SQLite-compatible write serialization and PostgreSQL native
  transactions.
- [x] Run the shared storage matrix for SQLite, local Turso, PostgreSQL, and the
  opt-in disposable managed-Turso profile.

---

### Task 6: Publish the adapter contract-test kit

**Files:**
- Move: `internal/adapters/conformance/*.go` to `contracttest/*.go`
- Modify: all adapter conformance test imports

- [x] Make suites accept public `port.UnitOfWork` factories only.
- [x] Keep environment provisioning, credentials, and self-hosted adapter
  construction out of the public package.
- [x] Export only stable suite entry points and option types.
- [x] Prove the same CRUD, transaction, lease, idempotency, and notification
  behavior for every supported relational profile.

---

### Task 7: Prove consumption from a separate Go module

**Files:**
- Create: `integration/testdata/external-module/go.mod`
- Create: `integration/testdata/external-module/external_test.go`
- Create: `integration/external_module_test.go`

- [x] Give the fixture an unrelated module path and a local `replace` used only
  by the fixture.
- [x] Import `domain`, `application`, `application/port`, and `contracttest`.
- [x] Implement an in-memory UnitOfWork/fake adapter and construct at least one
  application service.
- [x] From the harness, run `GOWORK=off go test ./...` in the fixture directory.
- [x] Reject accidental imports of Xisnove `internal` packages by relying on
  normal Go visibility rules.

---

### Task 8: Document and version the public contract

**Files:**
- Create: `docs/architecture/open-core.md`
- Modify: `README.md`
- Modify: `docs/development.md`
- Modify: release/check scripts as needed

- [x] Document the supported extension surface, dependency direction,
  UnitOfWork atomicity, context rules, and self-hosted composition root.
- [x] State that the public core is single-tenant and SaaS-blind.
- [x] Document separate future analytics ports and the non-goal of replacing
  operational persistence with ClickHouse.
- [x] Add compatibility guidance: exported package/API changes follow semantic
  versioning and `api/openapi.yaml` remains the HTTP source of truth.
- [ ] Add the canonical Apache 2.0 `LICENSE` and required notice material before
  any release metadata describes the project as Apache licensed.

---

### Task 9: Final verification and checkpoint

- [x] Run `gofmt` on changed Go files.
- [x] Run `go test -race ./domain ./application ./application/port ./contracttest`.
- [x] Run architecture and external-module tests with `GOWORK=off`.
- [x] Run `make check` and `git diff --check`.
- [x] Confirm `rg 'github.com/araihu/xisnove/internal/(domain|application|adapters/conformance)'`
  returns no Go imports.
- [x] Confirm the public domain contains no SaaS-specific concepts.
- [x] Commit and push the verified Open Core gate before resuming Milestone 3
  Task 3.
