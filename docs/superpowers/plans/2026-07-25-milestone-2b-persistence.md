# Milestone 2B Persistence Breadth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> `superpowers:executing-plans` to implement this plan task-by-task. Every
> production behavior follows red-green-refactor.

**Goal:** Run the existing HTTP/TCP/DNS observation path unchanged on SQLite,
local Turso Database, managed Turso Cloud, and PostgreSQL, with explicit schema
management, cross-adapter behavioral conformance, safe multi-replica claims,
and tested backup/restore procedures.

**Architecture:** Keep application ports and domain types database-neutral.
SQLite, local Turso, and managed Turso share one SQLite-compatible sqlc query
family and repository mapper. PostgreSQL has its own migrations, sqlc package,
and adapter mapper, including native `FOR UPDATE SKIP LOCKED` claims. A small
database-profile factory is the only place where server commands select a
driver, connection policy, migration implementation, and replica capability.
Kubernetes remains a client of the public API and is never a datastore.

**Tech Stack:** Go 1.26.1, `database/sql`, sqlc v1.31.1, Goose v3.27.3,
`modernc.org/sqlite` v1.54.0, `turso.tech/database/tursogo` v0.7.1,
`github.com/tursodatabase/libsql-client-go` at
`v0.0.0-20260528064733-9d5d30a29a60`, and `github.com/jackc/pgx/v5` v5.10.0.

## Global constraints

- `application.Store` and repository records remain the behavioral contract;
  no sqlc, driver, UUID, JSON, timestamp, or nullable type enters domain or
  application packages.
- Driver/profile selection is explicit. A path is never guessed to be a remote
  URL, and credentials are accepted through environment or secret files rather
  than emitted in logs or process diagnostics.
- SQLite and local Turso reject configurations that enable more than one active
  server. Managed Turso and PostgreSQL are marked replica-safe only after the
  competing-claim conformance tests pass.
- Every claim uses database time and compare-and-set or native row locking.
  Network I/O never occurs while a database transaction is open.
- Migrations run only through `xisnove-server db migrate`; normal startup checks
  exact schema compatibility and never migrates implicitly.
- The SQLite-compatible migration stream must execute on all three compatible
  drivers. PostgreSQL has a separate, semantically equivalent stream.
- Managed Turso integration tests require protected credentials and skip
  locally with an explicit reason. SQLite and local Turso run on every normal
  `make check`; PostgreSQL runs in CI against an ephemeral service.
- Backup artifacts never contain database credentials. Restore always targets a
  fresh instance and must pass schema readiness plus a first-observation smoke
  read.
- Preserve existing public API behavior and generated OpenAPI consumers.

---

### Task 1: Database profiles and lifecycle contract

**Files:**
- Create: `internal/adapters/database/profile.go`
- Create: `internal/adapters/database/profile_test.go`
- Create: `internal/adapters/database/open.go`
- Test: `internal/adapters/database/open_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Produces: `database.Profile`, `database.Config`, `database.Handle`
- Produces: `database.Open(context.Context, Config)`
- Consumes: existing SQLite `Open`, `Migrate`, `Ready`, and `NewStore`

- [ ] **Step 1: Write failing profile-validation tests**

Cover these literal cases:

```go
func TestConfigValidation(t *testing.T) {
    tests := []struct {
        name string
        cfg database.Config
        wantProfile database.Profile
        wantReplicaSafe bool
        wantErr string
    }{
        {"sqlite", database.Config{Profile: "sqlite", URL: "./xisnove.db"},
            database.ProfileSQLite, false, ""},
        {"local turso", database.Config{Profile: "turso-local", URL: "./xisnove.turso"},
            database.ProfileTursoLocal, false, ""},
        {"managed turso needs token", database.Config{
            Profile: "turso-cloud", URL: "libsql://example.turso.io",
        }, "", false, "token"},
        {"postgres", database.Config{
            Profile: "postgres", URL: "postgres://localhost/xisnove",
        }, database.ProfilePostgres, true, ""},
    }
    // Assert exact normalized profile/capability or stable validation class.
}
```

Also prove configuration redaction never returns a Turso token or PostgreSQL
password.

- [ ] **Step 2: Verify the tests fail**

Run:

```bash
go test ./internal/adapters/database -run 'Profile|Config|Redact' -v
```

Expected: compilation fails because the package does not exist.

- [ ] **Step 3: Implement the lifecycle boundary**

Define:

```go
type Profile string

const (
    ProfileSQLite    Profile = "sqlite"
    ProfileTursoLocal Profile = "turso-local"
    ProfileTursoCloud Profile = "turso-cloud"
    ProfilePostgres   Profile = "postgres"
)

type Config struct {
    Profile Profile
    URL string
    AuthToken string
}

type Handle struct {
    DB *sql.DB
    Store application.Store
    Profile Profile
    ReplicaSafe bool
    Migrate func(context.Context) error
    Ready func(context.Context) error
    Close func() error
}
```

Keep construction behind unexported per-profile openers. The first green
implementation may wire SQLite only and return a stable unsupported-profile
error for the other valid profiles; later tasks replace those stubs. Configure
connection pool sizes per profile and ping before returning.

- [ ] **Step 4: Pin current stable drivers**

Pin exactly:

```bash
go get turso.tech/database/tursogo@v0.7.1
go get github.com/tursodatabase/libsql-client-go@v0.0.0-20260528064733-9d5d30a29a60
go get github.com/jackc/pgx/v5@v5.10.0
go mod tidy
```

No testcontainers dependency is required; GitHub Actions provides PostgreSQL as
a service container.

- [ ] **Step 5: Run focused tests and commit**

```bash
go test -race ./internal/adapters/database
git add internal/adapters/database go.mod go.sum
git commit -m "feat(database): define persistence profiles"
```

---

### Task 2: Shared SQLite-compatible adapter and local Turso

**Files:**
- Create: `internal/adapters/sqlitecompat/database.go`
- Create: `internal/adapters/sqlitecompat/migrate.go`
- Move/modify: `internal/adapters/sqlite/store.go`
- Modify: `internal/adapters/sqlite/database.go`
- Modify: `internal/adapters/sqlite/migrate.go`
- Create: `internal/adapters/tursolocal/database.go`
- Test: `internal/adapters/tursolocal/database_test.go`
- Test: `internal/adapters/tursolocal/migrate_test.go`
- Modify: `internal/adapters/database/open.go`

**Interfaces:**
- Produces: one repository implementation backed by `db/generated/sqlite`
- Produces: local Turso profile using driver name `turso`
- Preserves: `sqlite.NewStore` compatibility while commands migrate to factory

- [ ] **Step 1: Write a failing local-Turso smoke test**

Against a temporary `.db`, prove:

1. `database.Open` selects `turso-local`;
2. all SQLite-compatible migrations apply;
3. `Ready` accepts exactly the latest schema;
4. a location and HTTP Monitor round-trip through `application.Store`;
5. close/reopen retains the rows.

Run:

```bash
go test ./internal/adapters/tursolocal -run 'Open|Migrate|RoundTrip' -v
```

Expected: FAIL because the adapter is missing.

- [ ] **Step 2: Extract the compatible repository mapper**

Move repository mapping, time/JSON conversion, transaction handling, and
constraint normalization into `sqlitecompat`. It accepts `*sql.DB` and generated
SQLite queries. Keep thin `sqlite.NewStore` and migration wrappers so existing
tests remain source-compatible.

Do not apply SQLite-only PRAGMAs in the shared package. SQLite keeps foreign
keys, WAL, and busy timeout in its opener. Local Turso receives only settings
documented and accepted by `tursogo`.

- [ ] **Step 3: Implement local Turso open/migrate/readiness**

Import `_ "turso.tech/database/tursogo"` and use:

```go
sql.Open("turso", cfg.URL)
```

Limit v1 local Turso to one open server connection. Execute the existing
SQLite-compatible Goose migrations and use the shared
`schema_migrations` readiness check. If an existing migration uses unsupported
syntax, first add a focused SQLite-upgrade regression, then rewrite that
unreleased migration to the smallest syntax supported by modern SQLite,
local Turso, and remote libSQL.

- [ ] **Step 4: Run all compatible-adapter tests**

```bash
go test -race ./internal/adapters/sqlite ./internal/adapters/sqlitecompat ./internal/adapters/tursolocal
```

Expected: PASS with no cgo requirement.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters
git commit -m "feat(database): add local Turso profile"
```

---

### Task 3: Persistence conformance harness

**Files:**
- Create: `internal/adapters/conformance/store.go`
- Create: `internal/adapters/conformance/claims.go`
- Create: `internal/adapters/conformance/migrations.go`
- Modify: `internal/adapters/sqlite/store_test.go`
- Modify: `internal/adapters/tursolocal/database_test.go`

**Interfaces:**
- Produces: `conformance.Run(t, Factory)`
- Consumes: only `application.Store`, profile migrate/ready callbacks, and
  fixture constructors

- [ ] **Step 1: Extract failing behavioral cases**

The same suite must prove:

- transaction callback errors roll back every write;
- duplicate result ID and duplicate run result are idempotent;
- one active Incident per Monitor is enforced;
- two goroutines cannot claim the same run;
- an expired lease becomes claimable with an incremented attempt;
- stale-health claims are compare-and-set;
- scheduler insertion and schedule advancement are duplicate-safe;
- migration from version 1 reaches the latest compatible schema;
- cleanup hooks operate in bounded batches once introduced.

Keep assertions in domain/application types; do not branch expectations by
driver.

- [ ] **Step 2: Run against SQLite and observe local Turso gaps**

```bash
go test -race ./internal/adapters/sqlite ./internal/adapters/tursolocal -run Conformance -count=10
```

Expected: SQLite passes; any local Turso incompatibility is a concrete failing
case, not a skipped assertion.

- [ ] **Step 3: Make the shared compatible adapter conform**

Normalize unique/foreign-key/not-found errors by semantic class. Add bounded
retry only for transient busy/locked errors and only around entire short
transactions; never retry a partially observed application callback.

- [ ] **Step 4: Commit**

```bash
git add internal/adapters
git commit -m "test(database): share persistence conformance suite"
```

---

### Task 4: PostgreSQL schema and generated query family

**Files:**
- Create: `db/migrations/postgres/00001_initial.sql`
- Create: `db/migrations/postgres/00002_protocol_breadth.sql`
- Create: `db/migrations/postgres/00003_staleness.sql`
- Create: `db/migrations/postgres/migrations.go`
- Create: `db/queries/postgres/*.sql`
- Modify: `sqlc.yaml`
- Generate: `db/generated/postgres/*.go`
- Test: `db/migrations/postgres/schema_test.go`

**Interfaces:**
- Produces: PostgreSQL schema semantically equivalent to SQLite version 3
- Produces: `db/generated/postgres`
- Consumes: existing application repository operations

- [ ] **Step 1: Add sqlc schema assertions**

Add a test that reads the embedded migration family and verifies all durable
tables, the partial one-active-Incident index, due-work indexes, and
`schema_migrations` ownership are represented. The test fails while the family
is absent.

- [ ] **Step 2: Author native PostgreSQL migrations**

Use `uuid`, `timestamptz`, `jsonb`, `bytea`, and `boolean` where they improve
validation. Keep migration version boundaries aligned with SQLite. PostgreSQL
down migrations must be explicit and ordered.

- [ ] **Step 3: Port queries and native claims**

Most queries preserve names and semantics. Use PostgreSQL placeholders and:

```sql
WITH candidate AS (
  SELECT r.id
  FROM check_runs r
  JOIN agents a ON a.id = sqlc.arg(agent_id)
  WHERE ...
  ORDER BY r.scheduled_for, r.id
  FOR UPDATE SKIP LOCKED
  LIMIT 1
)
UPDATE check_runs AS r
SET ...
FROM candidate
WHERE r.id = candidate.id
RETURNING r.*;
```

Use `clock_timestamp()` for `DatabaseNow`. Preserve compare-and-set stale claims
and `ON CONFLICT DO NOTHING` result idempotency.

- [ ] **Step 4: Generate and verify**

```bash
go tool sqlc generate
go tool sqlc diff
go test ./db/migrations/postgres
```

- [ ] **Step 5: Commit**

```bash
git add db sqlc.yaml
git commit -m "feat(postgres): generate native persistence schema"
```

---

### Task 5: PostgreSQL adapter and competing-replica tests

**Files:**
- Create: `internal/adapters/postgres/database.go`
- Create: `internal/adapters/postgres/migrate.go`
- Create: `internal/adapters/postgres/store.go`
- Create: `internal/adapters/postgres/database_test.go`
- Modify: `internal/adapters/database/open.go`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Produces: PostgreSQL implementation of `application.Store`
- Consumes: `db/generated/postgres` and shared conformance suite

- [ ] **Step 1: Add an environment-gated failing adapter test**

`XISNOVE_TEST_POSTGRES_URL` enables the suite. Absence skips with a literal
message. When present, create a unique schema per test, migrate it, run
conformance, and drop only that validated schema.

- [ ] **Step 2: Implement pgx database/sql and repository mapping**

Register pgx stdlib, configure a bounded pool, map UUID/time/JSON/null types at
the adapter boundary, and implement `WithinTx` using the transaction-bound sqlc
queries. Convert `pgx.ErrNoRows` and constraint errors to the same application
semantics as the compatible adapter.

- [ ] **Step 3: Prove multi-replica claims**

Open two independent pools to one schema. With at least 50 due runs and 8
concurrent claimers, assert each run is claimed once, no claimer blocks beyond
the test deadline, attempts increment correctly after expiry, and Incident
uniqueness survives competing transactions.

- [ ] **Step 4: Add PostgreSQL CI service**

Add a pinned PostgreSQL service image with a health check and pass its URL only
to the root test step. CI must run the conformance and competing-replica tests
on every pull request and `master` push.

- [ ] **Step 5: Verify and commit**

```bash
XISNOVE_TEST_POSTGRES_URL='postgres://...' go test -race ./internal/adapters/postgres -count=10
make check
git add internal/adapters .github/workflows/ci.yml go.mod go.sum
git commit -m "feat(postgres): implement replica-safe store"
```

---

### Task 6: Managed Turso Cloud adapter and protected conformance

**Files:**
- Create: `internal/adapters/tursocloud/database.go`
- Create: `internal/adapters/tursocloud/database_test.go`
- Modify: `internal/adapters/database/open.go`
- Create: `.github/workflows/turso-conformance.yml`
- Create: `docs/operations/turso-cloud.md`

**Interfaces:**
- Produces: remote libSQL profile over `database/sql`
- Consumes: shared SQLite-compatible store, migrations, and conformance suite

- [ ] **Step 1: Write URL/token construction tests**

Prove token escaping, existing query preservation, rejection of non-libSQL
remote schemes, pool bounds, and complete redaction. Never compare or print a
real token.

- [ ] **Step 2: Implement the remote profile**

Import `_ "github.com/tursodatabase/libsql-client-go/libsql"` and construct a
libSQL DSN using structured URL operations. Use the shared SQLite-compatible
queries. Because the managed HTTP transport cannot reuse Goose's pinned
connection after a request, apply each shared embedded migration as one atomic
libSQL batch together with its version record. Retry only transient remote
lock/serialization failures with bounded jitter and context cancellation.

- [ ] **Step 3: Add opt-in live conformance**

Require both `XISNOVE_TEST_TURSO_URL` and `XISNOVE_TEST_TURSO_TOKEN`. Use a
dedicated disposable database configured by the workflow; never delete or
branch an arbitrary user database. Run migrations, shared conformance,
competing claims from two pools, and readiness.

- [ ] **Step 4: Add protected scheduled/release workflow**

The workflow runs on manual dispatch, a schedule, and release candidates. It
uses repository secrets, concurrency control, timeouts, and emits no DSN. Normal
pull requests compile the adapter and run all non-secret tests.

- [ ] **Step 5: Verify and commit**

```bash
go test -race ./internal/adapters/tursocloud
make check
git add internal/adapters .github/workflows/turso-conformance.yml docs
git commit -m "feat(turso): add managed Cloud profile"
```

---

### Task 7: Profile-aware server commands and readiness

**Files:**
- Modify: `cmd/xisnove-server/database.go`
- Modify: `cmd/xisnove-server/admin.go`
- Modify: `cmd/xisnove-server/serve.go`
- Modify: `cmd/xisnove-server/main_test.go`
- Create: `cmd/xisnove-server/config_test.go`
- Modify: `docs/development.md`

**Interfaces:**
- Produces: `--database-profile`, `--database-url`, and
  `--database-auth-token-file`
- Consumes: `database.Open`

- [ ] **Step 1: Write failing command/config tests**

Prove all three commands use the same profile parser; old
`--database /path.db` remains a deprecated SQLite alias during v1; token files
are trimmed and permission errors are stable; errors and readiness responses do
not expose URLs with credentials.

- [ ] **Step 2: Route commands through the factory**

Migration, bootstrap, and serve each open one `database.Handle`. Serve rejects a
requested replica count greater than one for SQLite/local Turso. Readiness calls
the selected profile’s exact schema check. Shutdown closes loops, listener, and
then the handle.

- [ ] **Step 3: Run command and integration regressions**

```bash
go test -race ./cmd/xisnove-server ./integration
```

- [ ] **Step 4: Commit**

```bash
git add cmd docs/development.md
git commit -m "feat(server): select relational database profiles"
```

---

### Task 8: Backup, restore, and migration smoke tests

**Files:**
- Create: `internal/adapters/backup/backup.go`
- Create: `internal/adapters/backup/sqlite.go`
- Create: `internal/adapters/backup/backup_test.go`
- Modify: `cmd/xisnove-server/main.go`
- Create: `cmd/xisnove-server/backup.go`
- Create: `docs/operations/backup-restore.md`
- Create: `integration/backup_restore_test.go`

**Interfaces:**
- Produces: `xisnove-server db backup`
- Produces: documented restore procedures per profile
- Consumes: profile metadata and readiness

- [ ] **Step 1: Write a failing SQLite backup/restore integration**

Create a live SQLite database with an administrator, monitor, result, health,
and Incident. Back it up while reads continue, restore to a fresh path, open the
restored database, require exact schema readiness, and compare stable row
counts/IDs.

- [ ] **Step 2: Implement online compatible backup**

For SQLite, use the driver-supported online backup API or `VACUUM INTO` with a
validated non-existing destination and restrictive permissions. For local
Turso, run the same test; if upstream lacks an online primitive, fail closed and
document the supported quiesced procedure rather than copying a live WAL.

Managed Turso backup/restore uses the provider’s current database backup or
branch procedure and a disposable restore smoke database. PostgreSQL uses
`pg_dump --format=custom` and `pg_restore` in documented operational steps; the
application must not parse or rewrite those artifacts.

- [ ] **Step 3: Add migration and restore CI smoke**

Exercise fresh creation and version-1 upgrade for every locally available
profile. PostgreSQL CI additionally performs dump, restore into a fresh
database, migrate/readiness, and a first-observation read.

- [ ] **Step 4: Verify and commit**

```bash
go test -race ./integration -run 'BackupRestore|MigrationUpgrade'
make check
git add internal/adapters/backup cmd integration docs
git commit -m "feat(database): verify backup and restore workflows"
```

---

### Task 9: Broad cross-profile storage integration matrix

**Files:**
- Create: `integration/storage_matrix_test.go`
- Create: `integration/storage_journey_test.go`
- Create: `internal/testsupport/tursocloud/provision.go`
- Create: `internal/testsupport/tursocloud/provision_test.go`
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/turso-conformance.yml`

**Interfaces:**
- Produces: one storage journey with identical operations and assertions
- Produces: disposable managed-Turso provisioning with verified teardown
- Consumes: all four `database.Handle` implementations and the public
  application services

- [ ] **Step 1: Define one literal storage journey**

The test body must not branch by profile after it receives a migrated Handle.
For every supported profile, execute the same ordered operations:

1. migrate and require exact readiness;
2. bootstrap an administrator and create/authenticate a session;
3. create a Location and HTTP, TCP, and DNS Monitors with assignments;
4. create and consume an Agent enrollment token, enroll the Agent, and record a
   heartbeat;
5. enqueue due work, claim each protocol from a second independent Handle or
   pool, resolve leases, and ingest one mixed result batch;
6. read persisted protocol observations, per-location/aggregate health, and the
   active Incident;
7. retry the same batch and scheduler tick and assert no duplicate result,
   projection, run, or IncidentEvent;
8. force lease expiry and stale-health deadlines through repository inputs and
   assert safe reclaim and one warning unknown transition;
9. inject a transaction callback failure and prove all writes rolled back;
10. close/reopen and assert the same durable state and exact schema version.

Use fixed UUID fixtures and UTC instants. Compare domain/application values,
stable error classes, row counts, and transitions; never compare
driver-specific types or messages.

- [ ] **Step 2: Run the journey on all locally available profiles**

SQLite and local Turso always run. PostgreSQL uses Testcontainers to provision
PostgreSQL 18 when a healthy container runtime is available, accepts
`XISNOVE_TEST_POSTGRES_URL` as an external-server override, and is mandatory in
normal CI. Each profile starts empty and owns its isolated path/schema.
PostgreSQL uses two independent pools during the same journey to prove replica
visibility.

- [ ] **Step 3: Provision a real managed Turso database**

The test support package uses the official Platform API with a token supplied
only through `TURSO_API_KEY` or the protected CI secret. It:

- lists accessible organizations and requires an explicit organization when
  more than one is available;
- requires a dedicated configured CI group whose delete protection is disabled,
  and fails before database creation if the group is protected;
- never changes delete protection or creates a database in a shared production
  or application group;
- creates a unique `xisnove-ci-<timestamp>-<random>` database in that dedicated
  configured group;
- records the returned database name, ID, and hostname before any test runs;
- mints a short-lived database-scoped full-access JWT;
- supplies `libsql://<returned-hostname>` and the JWT only in memory;
- deletes only the exact created database in `t.Cleanup`;
- retries deletion with a deadline, then lists by exact name to prove absence;
- redacts platform tokens, database JWTs, and authenticated URLs from every
  error and log.

Creation failure performs no deletion. A post-create test failure must still
run cleanup. Local development may load the user-owned root `.env` outside the
test binary; `.env` is never parsed by production code, copied into a worktree,
or committed.

- [ ] **Step 4: Run the identical journey on managed Turso**

No managed-Turso assertion may be skipped or weakened. Use a second independent
remote pool for competing claims and visibility. The test passes only after the
database is confirmed deleted.

```bash
TURSO_API_KEY='<loaded without printing>' \
  go test -race ./integration -run TestStorageMatrix/TursoCloud -v
```

- [ ] **Step 5: Gate CI and commit**

Normal CI runs SQLite, local Turso, and PostgreSQL matrix cases. The protected
scheduled/release workflow runs the same test binary with real Turso Platform
credentials. Store JUnit output, but never database URLs or tokens.

```bash
go test -race ./integration -run TestStorageMatrix -count=10
make check
git add integration internal/testsupport .github/workflows
git commit -m "test(database): verify cross-profile storage journey"
```

---

### Task 10: Complete milestone 2B operational and release gate

**Files:**
- Modify: `README.md`
- Modify: `docs/operations/first-observation.md`
- Create: `docs/operations/database-profiles.md`
- Create: `docs/operations/persistence-conformance.md`
- Modify: `.github/workflows/ci.yml`
- Modify: `Makefile`

- [ ] **Step 1: Document the exact capability matrix**

Include driver/version, CGO status, active-server limit, migration family,
backup/restore method, credential inputs, CI coverage, and evolving/stable
status. Explicitly distinguish local Rust Turso Database from managed libSQL
Turso Cloud and state that local sync is not a lease coordinator.

- [ ] **Step 2: Make local conformance part of `make check`**

SQLite and local Turso run unconditionally. PostgreSQL tests self-provision via
Testcontainers when a container runtime is available, accept an external URL
override, and are mandatory in CI. Managed Turso tests remain a protected
scheduled/release gate.

- [ ] **Step 3: Run final milestone verification**

```bash
make check
go test -race ./internal/adapters/sqlite ./internal/adapters/tursolocal -run Conformance -count=20
XISNOVE_TEST_POSTGRES_URL='postgres://...' \
  go test -race ./internal/adapters/postgres -run 'Conformance|Competing' -count=20
go test -race ./integration -run 'FirstObservation|ProtocolBreadth|BackupRestore|StorageMatrix' -count=10
git status --short --branch
```

Expected:

- one public API/application behavior on four persistence profiles;
- deterministic generated SQLite-compatible and PostgreSQL query families;
- safe duplicate, lease-expiry, stale-health, rollback, and competing-claim
  behavior;
- exact schema readiness and explicit migrations;
- tested restore smoke for self-managed profiles;
- no credential leakage;
- clean root and Agent generation/vet/race gates.

- [ ] **Step 4: Commit**

```bash
git add README.md docs .github/workflows/ci.yml Makefile
git commit -m "docs(database): complete persistence operations"
```

---

## Final milestone 2B verification

Run from a clean worktree:

```bash
make check
git diff --check
git status --short --branch
```

Then verify the PostgreSQL service-backed and protected managed-Turso workflows
before a release. Do not claim managed Turso conformance from mocked or local
libSQL behavior.

After this milestone lands, continue with milestone 3: transactional
notification outbox, the current stable `nicholas-fedor/shoutrrr` fork,
Alertmanager, routing, maintenance, retention, metrics, tracing, and expanded
graceful-readiness behavior.
