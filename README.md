# Xisnove

Xisnove is an API-first, cloud-native monitoring system written in Go. The
current milestone supports HTTP/TLS, TCP, and resolver-pinned DNS monitors
stored in SQLite, local Turso Database, managed Turso Cloud, or PostgreSQL;
work is leased or delivered by capability to outbound Agents, projected into
health, and promoted into Incidents after failures or durable staleness.
Incident changes
can be routed through an encrypted transactional outbox to Shoutrrr or
Alertmanager, with durable attempts, bounded retries, and explicit replay.
Maintenance explicitly chooses between pausing observation (which projects
dependent monitors to `unknown`) and suppressing delivery while probes
continue. State ticks retain reason, actor, user-action, and causal history;
bounded retention publishes daily uptime aggregates. Structured logs, bounded
metrics, optional tracing, readiness, and ordered shutdown provide the
operations surface for a self-hosted installation.

The repository also contains separately buildable UI/BFF, CLI, Agent, and
Kubernetes operator modules. The
UI consumes the public generated SDK and never accesses storage directly; the
CLI provides profile, authentication, monitor, location, Agent, incident,
discovery, notification, maintenance, and public-status workflows through that
same SDK. The operator reconciles Monitor and Agent CRDs into the external,
relationally backed control plane; an in-cluster Agent can discover Services,
Ingresses, and HTTPRoutes without moving monitoring history into Kubernetes.
Release packaging is the active milestone.

The public contract is [api/openapi.yaml](api/openapi.yaml). Root code contains
the control plane, public Go SDK, and importable Open Core surface in `domain`,
`application`, `application/port`, and `contracttest`; self-hosted adapters stay
internal. `agent/`, `cli/`, `operator/`, and `ui/` are independently buildable
Go modules. Release builds use the repository workspace so every consumer
resolves the SDK and OpenAPI-generated code from the same checkout.

- [canonical monitoring architecture](docs/superpowers/specs/2026-07-24-xisnove-v1-design.md)
- [Open Core extension surface](docs/architecture/open-core.md)
- [milestone implementation plan](docs/superpowers/plans/2026-07-24-milestone-1-first-observation.md)
- [protocol breadth plan](docs/superpowers/plans/2026-07-25-milestone-2a-protocol-breadth.md)
- [notifications and operations plan](docs/superpowers/plans/2026-07-25-milestone-3-notifications-operations.md)
- [development guide](docs/development.md)
- [first observation runbook](docs/operations/first-observation.md)
- [notification operations](docs/operations/notifications.md)
- [maintenance and retention](docs/operations/maintenance-retention.md)
- [observability and lifecycle](docs/operations/observability.md)
- [database profiles](docs/operations/database-profiles.md)
- [persistence conformance](docs/operations/persistence-conformance.md)
- [backup and restore](docs/operations/backup-restore.md)
- [release versioning](docs/distribution/versioning.md)
- [compatibility policy](docs/distribution/compatibility.md)
- [artifact matrix](docs/distribution/artifact-matrix.md)
- [runtime contracts](docs/distribution/runtime-contracts.md)
- [database profile matrix](docs/distribution/database-profile-matrix.md)
- [secret and bootstrap reference matrix](docs/distribution/secret-reference-matrix.md)

Quick verification:

```bash
make check
make distribution-contract-check
make storage-check
make operations-check
make cli-check
make cli-workspace-check
```

`make storage-check` uses SQLite and local Turso directly. PostgreSQL uses the
`XISNOVE_TEST_POSTGRES_URL` override when supplied, or a disposable PostgreSQL
18 Testcontainer when a healthy container runtime is available. Managed Turso
is exercised separately by the protected manual or weekly workflow, which is
also reusable as a required pre-publication release gate. It refuses protected
groups, provisions one disposable database, runs the same cross-profile
journey (including maintenance and retention), uploads JUnit, and verifies
teardown from an independent cleanup job even when tests time out.
