# Observable Monitoring BFF Contract and History Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` when executing this plan.

**Goal:** Replace the legacy single-location monitoring seam with an executable, reviewable contract for canonical observable history, then deliver the first backend and Goshtoso BFF slice through paired implementation and independent review loops.

**Architecture:** The OpenAPI document remains the only application boundary. The first checkpoint freezes a monitor-scoped state-tick history read model and a deterministic strict mock server fixture so the BFF can be developed without inventing backend behavior. Backend workers then add the domain/application/storage projection and HTTP handler. Frontend workers consume only the generated SDK through the BFF and render the bounded history in the monitor drawer. Root integrates only frozen commits and repeats correction/review waves on exact candidate identities.

**Tech Stack:** Go, OpenAPI 3.1.2, oapi-codegen strict server/SDK, relational repositories/sqlc, `internal/mockapi`, server-rendered templ/HTMX, Goshtoso Charts, Go tests.

**Spec:** `docs/superpowers/specs/2026-07-24-xisnove-v1-design.md` is canonical. This slice implements the contract needed to expose immutable StateTicks (lifecycle, health, reason, action, actor, occurrence time, and causal links) for one monitor over a bounded UTC window. Probe-sample availability history remains available for the chart; StateTick history is the authoritative provenance stream.

## Global Constraints

- Frozen base for this plan: `daf37f996b8eb8651b07983166bc6dfff3743504` on `codex/milestone-4a-control-plane`; the root worktree is clean before each checkpoint.
- Root owns contract scaffolding, integration, candidate freezing, lifecycle, and acceptance. Implementers must not edit the other lane's owned paths, generated files, or the canonical spec.
- The scaffold must pass contract tests, generated drift checks, mock-server tests, and `git diff --check` before review dispatch.
- Agents work from isolated worktrees branched at the exact frozen checkpoint and return commit SHA, tree, status, tests, and any concerns. Do not reset or revert another worker's changes.
- Reviewers are read-only and receive an immutable candidate SHA/tree plus this plan. Any correction invalidates prior review verdicts and requires a fresh freeze.
- State history is bounded to at most three hours and 10,000 records; windows are UTC half-open `[startsAt, endsAt)` and reject future, reversed, or over-wide requests.
- `unknown` represents missing/administrative/dependency uncertainty, not a failed probe. Stable `reasonCode`, `actionId`, `actor`, and `occurredAt` are never reconstructed from free-form text.

---

## Checkpoint 0 — Executable contract and mock scaffold (root-owned)

**Purpose:** Freeze the public seam before any backend or frontend writer starts.

**Owned paths:** `api/openapi.yaml`, `api/milestone4_contract_test.go` (only new operation assertions), `internal/mockapi/advertised.go`, `internal/mockapi/server.go`, `internal/mockapi/resources.go`, `internal/mockapi/server_test.go`, generated contract outputs (`internal/adapters/httpapi/generated.gen.go`, `internal/mockapi/generated.gen.go`, `sdk/generated.gen.go`).

**Contract requirements:**

1. Add `GET /v1/monitors/{monitorId}/state-ticks` (`getMonitorStateHistory`) with monitor-read authorization and the existing bounded UTC history parameters.
2. Add `MonitorStateHistory`, `MonitorStateTick`, `StateTickActor`, `MonitorLifecycle`, and `StateTickReasonCode` schemas. A tick carries `id`, `monitorId`, optional `locationId`, `lifecycle`, `health`, `reasonCode`, `actionId`, actor kind/optional ID, `occurredAt`, and optional `userActionId`, `observationId`, and `causalTickId`.
3. Keep `MonitorAvailabilityHistory` probe samples intact; the two histories are intentionally distinct so an absence is not silently converted into a failure.
4. Seed the strict mock with at least three chronologically ordered ticks spanning `up`, `degraded`, and `unknown`, including a user action and a causal dependency tick. The fixture must honor bearer scope checks and return an empty valid history for an unknown monitor only through the contract's normal not-found behavior.
5. Add a mock integration test that requests the endpoint with the fixture token and verifies ordering, reason/action/actor provenance, bounded envelope fields, and unauthorized behavior.

**Gates:** `go test ./api ./internal/mockapi`, `go generate ./api ./sdk`, `git diff --exit-code` after generation, `go test ./internal/mockapi ./sdk`, `go vet ./api ./internal/mockapi`, `git diff --check`.

**Review gate:** dispatch one independent scaffold reviewer (`gpt-5.6-sol`, high) with no write capability. Only a sealed `ACCEPT` on the exact scaffold SHA unlocks implementation lanes.

## Checkpoint 1 — Paired backend implementation (four-way work starts only after Checkpoint 0 review)

### Backend A — domain/application contract and projection

**Owns:** new StateTick value types and reason/actor validation under `domain/`; application history service and public ports under `application/` and `application/port/`; focused domain/application tests.

**Must prove:** immutable tick shape, bounded window validation, causal/user-action preservation, deterministic ordering, and unknown/maintenance/paused reasons without changing probe outcome semantics.

### Backend B — persistence and HTTP adapter

**Owns:** schema migration, sqlite/postgres queries and generated SQL, adapter repository implementation, `internal/adapters/httpapi` handler/mapping, integration/API tests. It consumes Backend A's frozen ports and must not redefine them.

**Must prove:** read-only history query is bounded and ordered identically across supported relational profiles, strict generated handler maps every field without leaking diagnostics, and not-found/validation/auth failures use the existing problem envelope.

**Integration order:** Backend A commit first, then Backend B rebased/cherry-picked onto it; root runs generated/storage/API gates and freezes one exact candidate.

## Checkpoint 2 — Paired frontend/BFF implementation

### Frontend A — BFF control-plane client and routes

**Owns:** `ui/internal/controlplane/**` and `ui/internal/web/**` plus focused tests. It consumes only the generated SDK and adapts the monitor state-history response to authenticated BFF fragments/SSE without exposing bearer credentials to browser JavaScript.

### Frontend B — view/chart presentation

**Owns:** `ui/internal/view/**`, `ui/internal/availability/**`, templated UI tests, and browser smoke assertions. It renders monitor name/lifecycle/health in the drawer title, shows the bounded history right-aligned with thin bars, uses neutral styling for `unknown`, and uses the warning palette for `degraded`.

**Integration order:** Frontend A and B may develop in parallel from the frozen backend/API candidate, then root integrates their disjoint commits and runs the UI generation, focused tests, race tests, and representative browser smoke.

## Checkpoint 3 — Independent reviews and correction loop

1. Freeze the integrated candidate with exact base/head/tree/status and changed-path manifest.
2. Dispatch Backend Reviewer and Frontend Reviewer as separate read-only `gpt-5.6-sol`/high agents. Each reviews only its lane against the canonical spec, plan, generated closure, security boundaries, tests, and runtime evidence.
3. Each returns exactly one sealed verdict: `ACCEPT`, `REJECT`, or `BLOCKED_NO_VERDICT`, with severity-classified findings and required gates.
4. Any `REJECT` produces a bounded correction packet to the owning lane. Corrected code gets a new SHA/tree and both prior verdicts are invalidated; rerun only the affected review plus a final cross-lane sanity review when interfaces changed.
5. Repeat for at most five review rounds. Stop as `BLOCKED_NO_VERDICT` with an explicit user-facing blocker if the same gate cannot be made verifiable; never claim completion on timeout or stale review evidence.

## Final acceptance

Root records both reviewer `ACCEPT` verdicts on the same frozen candidate, runs the combined gates (`make generate`, focused backend/storage/API/UI tests, `go test -race` for changed modules, `make ui-check`, `git diff --check`, and the representative BFF/browser smoke), updates the control-plane ledger, and reports exact commit/tree/test evidence. Checkpoint commits may be pushed under the standing user instruction to commit and push each checkpoint; merge/release/tag remains separately gated.
