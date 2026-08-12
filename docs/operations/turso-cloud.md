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

The `managed Turso conformance` workflow runs weekly, manually, and for release
events. It runs both repository conformance and the same full storage journey
used for SQLite, local Turso, and PostgreSQL, then retains a JUnit report.
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
set the same non-secret organization and dedicated group values. The Dagger
path uses the same conformance function as CI and keeps the token in a Dagger
`Secret`:

```bash
export TURSO_API_KEY='<redacted>'
export TURSO_ORG='your-organization'
export TURSO_GROUP='xisnove-ci'
DAGGER_EVENT_NAME=local DAGGER_RUN_ID="$(date +%s)" DAGGER_RUN_ATTEMPT=1 \
  bash scripts/materialize-dagger-input.sh turso .dagger-inputs/turso-local.json
dagger call turso-conformance --source=. \
  --input=.dagger-inputs/turso-local.json \
  --turso-api-key=env:TURSO_API_KEY \
  export --path=turso-output
test "$(cat turso-output/status)" = 0
```

The local input selects a local cache namespace. CI cache isolation is selected
by the host-owned runner lane, not by this JSON metadata.

Alternatively, `XISNOVE_TEST_TURSO_URL` and `XISNOVE_TEST_TURSO_TOKEN` may be
set together to run against a database whose lifecycle is managed externally.
That mode never deletes the supplied database.
