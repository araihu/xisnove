# Managed Turso Cloud

The `turso-cloud` database profile connects Xisnove to a managed Turso libSQL
database through `database/sql`. It supports multiple stateless control-plane
replicas because every scheduler, work, and staleness claim is coordinated by
durable database state. It is distinct from the single-process `turso-local`
profile.

Supply the database URL and authentication token separately. The URL must use
the `libsql` scheme and must not contain user information:

```text
database profile: turso-cloud
database URL:     libsql://xisnove-example.aws-us-east-1.turso.io
authentication:   database-scoped token file or secret injection
```

Never put a database JWT or Platform API token in a command argument, URL,
container image, manifest, or log. The server command accepts a token file so
Kubernetes Secrets, External Secrets Operator, Vault, or OpenBao can
materialize the credential without changing Xisnove's storage interface.

## Migrations and readiness

Managed Turso uses the same embedded SQLite-compatible migration files as the
SQLite and local Turso profiles. The libSQL HTTP transport closes its stream
after an individual request, which is incompatible with Goose's
connection-pinned provider lifecycle. Xisnove therefore sends each migration
as one atomic libSQL batch together with its schema-version record. Readiness
requires both a successful ping and the exact current schema version.

Run migrations before starting serving replicas. Do not start two independent
migration jobs concurrently. Application replicas may start concurrently only
after the migration job succeeds.

## Protected conformance workflow

The `managed Turso conformance` workflow runs weekly, manually, and as a
reusable `workflow_call` gate that a release workflow must complete before it
publishes. It runs both repository conformance and the same full storage
journey used for SQLite, local Turso, and PostgreSQL, then retains a JUnit
report. Its cleanup is a separate bounded job, so a test-job timeout still
attempts deletion and verifies absence.
Configure:

- repository secret `TURSO_API_KEY`, scoped to the test organization;
- repository variable `TURSO_ORG` when the token can see multiple
  organizations;
- repository variable `TURSO_GROUP`, naming a dedicated CI-only group.

The CI group must have delete protection disabled. The test preflights that
property and refuses to create a database in a protected or missing group. It
never changes group protection. Each test creates one cryptographically unique
`xisnove-ci-*` database, mints a ten-minute database-scoped token, deletes only
that exact database, and polls until absence is confirmed.

For a local protected run, load the Platform API token without printing it and
set the same non-secret organization and dedicated group values:

```bash
TURSO_API_KEY='<redacted>' \
TURSO_ORG='your-organization' \
TURSO_GROUP='xisnove-ci' \
go test -race ./internal/adapters/tursocloud -run Conformance -count=1
go test -race ./integration -run 'TestStorageMatrix/TursoCloud' -count=1
```

Alternatively, `XISNOVE_TEST_TURSO_URL` and `XISNOVE_TEST_TURSO_TOKEN` may be
set together to run against a dedicated scratch database whose lifecycle is
managed externally. Because both suites delete all Xisnove rows between
journeys, this mode also requires `XISNOVE_TEST_TURSO_ALLOW_RESET=1`. It never
deletes the supplied database itself, but it is destructive to its Xisnove
table contents.
