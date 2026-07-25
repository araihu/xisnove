# Persistence conformance

Xisnove tests storage behavior at two levels. Adapter conformance exercises
individual repository operations. `TestStorageMatrix` then runs one literal
application journey; profile setup occurs outside the journey, and the journey
contains no profile-specific branches or weakened assertions.

Every profile must perform the same sequence:

1. migrate and require the exact schema version;
2. bootstrap an administrator and create, authenticate, and expire a session;
3. create one Location plus assigned HTTP, TCP, and DNS Monitors;
4. expire and consume an enrollment token, enroll an Agent, and persist its
   heartbeat;
5. schedule three checks, observe an idempotent scheduler tick, and claim work
   through a second independent handle;
6. upload one mixed protocol result batch and read the exact observations,
   health projection, and critical Incident;
7. replay the batch and scheduler tick without adding results, runs, or
   IncidentEvents;
8. reclaim a lease across a fractional timestamp boundary and apply one
   compare-and-swap stale transition to a warning `unknown` Incident;
9. inject a transaction callback error and prove the write rolled back; and
10. close and reopen the database, require readiness, and compare durable IDs,
    row counts, monitors, results, health, and Incidents.

Fixed UUIDs and UTC instants keep values comparable. Assertions use domain and
application values and stable error classes, never driver error strings or
driver-specific scanned types.

## Local and PostgreSQL runs

SQLite and local Turso always run as part of `make check`:

```bash
make storage-check
```

PostgreSQL is opt-in locally and mandatory in normal CI. The test creates and
drops one random schema and opens two independent pools against it:

```bash
XISNOVE_TEST_POSTGRES_URL='postgres://postgres:password@127.0.0.1:5432/xisnove?sslmode=disable' \
  go test -race ./integration -run TestStorageMatrix -count=10
```

## Managed Turso run

Managed Turso uses a protected workflow because the test creates a real remote
database. `TURSO_GROUP` must identify a dedicated CI group with delete
protection disabled. Provisioning fails before creation for a protected or
missing group; it never changes group protection. Cleanup closes both remote
pools, deletes only the recorded database identity, polls for a `404`, and
fails the test if exact absence cannot be proven.

```bash
TURSO_API_KEY='<loaded without printing>' \
TURSO_ORG='your-organization' \
TURSO_GROUP='xisnove-ci' \
  go test -race ./integration -run 'TestStorageMatrix/TursoCloud' -count=1
```

The test binary never parses `.env`. Load a local environment file in the
calling shell, and never commit or copy it into a worktree. See [managed Turso
Cloud](turso-cloud.md) for workflow and credential details.
