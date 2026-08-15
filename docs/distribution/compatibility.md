# Compatibility contract

Xisnove applies semantic versioning to exported Go APIs, the public OpenAPI
contract, CLI scripts, CRDs, configuration, database schema, and deployment
artifacts. Additive OpenAPI fields remain optional within a major version.
Removing operations, tightening accepted input, or changing response meaning
requires a major version unless a security fix makes compatibility unsafe.

## Runtime and database window

Release N after the M6.1 baseline must support the immutable N-1
database/schema fixture frozen during M6.1. Expand migrations run before new
replicas and remain readable by both N-1 and N. Contract migrations run
separately only after every live N-1 process lease is gone. Readiness accepts
the documented schema interval and fails closed outside it.

The frozen portable fixture has explicit revisions under
`integration/testdata/migration-n-minus-one`: the original M6.1 fixture remains
at the root with manifest format 1 and schema interval `[10,11]`; the active
SQLite/local-Turso state-ticks revision is `v2/`, with manifest format 2 and
interval `[11,12]`.
The v2 revision is a separate checksum-anchored standalone probe, not a silent
mutation of the M6.1 artifact. Its gate creates schema 11, expands through the
state-ticks migration to schema 12, and proves both the v2 N-1 probe and the
current runtime remain ready. Any fixture or manifest change requires an
explicit revision, checksum, and compatibility-contract update.

The first upgrade from a pre-M6.1 binary is a singleton downtime transition:
stop every old server, back up schema 10, run the M6.1 expand migration to
schema 11, then start M6.1. Pre-M6.1 binaries accepted only exact schema 10;
rollback after that migration therefore restores the schema-10 backup. For the
SQLite/local-Turso state-ticks revision, start from a schema-11 backup, run the
schema-12 expand migration, and keep the v2 N-1 probe and current runtime
ready before the contract phase. Rolling mixed-version guarantees begin with
the M6.1 baseline and its explicit v2 revision.

PostgreSQL's additive state-tick revision is tracked separately as the
`integration/testdata/migration-n-minus-one/v3-postgres` v3 N-1 fixture: it
expands schema 12 to schema 13, keeps the legacy
`occurred_at` timestamp readable, and adds the exact nanosecond ordering key
and compatibility trigger before the contract phase.

PostgreSQL and managed Turso allow N-1 and N replicas during the expand window.
SQLite and local Turso use a singleton downtime upgrade: stop old process,
acquire ownership, migrate, then start new process. They never claim rolling
upgrade support.

Rollback to N-1 is supported after an expand migration while its schema remains
inside N-1's readable interval. After a contract migration, binary rollback is
unsupported; restore a pre-contract backup and then start the older release.
Migration failure never starts the application.

## Client and Kubernetes contracts

The UI BFF and CLI consume the public OpenAPI SDK. Same-major clients may talk
to servers whose required operations and fields they understand; clients must
preserve unknown response fields when proxying. The edge operator may add CRD
fields and status conditions, but removal or incompatible meaning requires a
new served/storage version and Kubernetes conversion plan.

Release notes state supported server/client, schema, Kubernetes, glibc, and
deployment ranges. Exact upgrade and rollback evidence accompanies each
candidate; absence of evidence is not compatibility.
