# Open Core extension surface

Xisnove's public module remains `github.com/araihu/xisnove`. It is a complete,
single-tenant, self-hosted monitoring system, not a reduced edition. A future
private `github.com/araihu/xisnove-cloud` module may import the public core and
provide its own adapters and composition roots; that SaaS product is not part
of v1.

The supported Go extension surface is:

- `domain`: entities, value types, validation, and pure transitions;
- `application`: use cases and constructors;
- `application/port`: infrastructure records, repositories, and UnitOfWork;
- `contracttest`: reusable behavioral suites for adapter implementations.

Self-hosted implementations remain in `internal/adapters`. The
`cmd/xisnove-server` command wires those implementations together, but it does
not own business behavior and is only one possible composition root.

## Dependency and context rules

Domain code imports neither application nor infrastructure. Application code
may import domain and public ports, but cannot import self-hosted adapters,
generated API handlers, sqlc packages, or database drivers. Adapters translate
their external/generated types at the boundary.

Application operations and ports take `context.Context` first. Services pass
the incoming context through `UnitOfWork.View` or `UnitOfWork.Transact`; the
callback receives that same context. Context propagation allows a future
composition root to carry validated request scope, but it is not authorization
by itself.

The public core intentionally has no tenant, organization, subscription,
billing, or entitlement concepts. A future hosted composition root is
responsible for authentication, authorization, tenant-scoped persistence,
cache/object keys, idempotency keys, and worker context construction.

## Stable public identifiers

The supported surface is intentionally small at its seams, while the domain
and application types needed to construct a self-hosted or external
composition root remain public. Exported Go identifiers follow semantic
versioning; external consumers should use `errors.Is` for documented error
identities rather than matching error strings.

- `domain` owns the ID/value types and pure constructors:
  `NewLocation`, `NewAgent`, `NewHTTPMonitor`, `NewTCPMonitor`,
  `NewDNSMonitor`, `NewMaintenanceInterval`, `NewNotificationChannel`,
  `NewNotificationRoute`, and `NewNotificationIdentity`.
- `application/port.UnitOfWork` and
  `application/port.Repositories` are the operational transaction boundary
  and its callback-scoped repository set. The repository interfaces and record
  types in `application/port` are part of that operational contract.
  `application/port.ErrNotFound` and `application/port.ErrConflict` retain
  their error identity for portable adapter behavior.
- `application` owns use-case commands, views, service configuration structs,
  and service constructors. `NewConfigurationService`, `NewAuthService`,
  `NewAgentService`, `NewLeaseService`, `NewResultService`,
  `NewHealthService`, `NewStalenessService`, `NewScheduler`,
  `NewNotificationAdminService`, `NewNotificationSecretService`,
  `NewDeliveryWorker`, `NewMaintenanceWorker`, and `NewRetentionWorker`
  preserve their documented dependency-injection and validation behavior.
  Error identities exported by these services include the authentication,
  enrollment, no-work, notification-key/lease, maintenance-lease, and
  retention-lease errors; callers may keep using `errors.Is` across releases.
- `contracttest.Factory` accepts only an
  `application/port.UnitOfWork`, and `contracttest.Run` is the stable adapter
  behavioral-suite entry point. Adapter provisioning and credentials stay in
  the caller's factory.

The application compatibility aliases for `application/port` records,
repositories, `UnitOfWork`, `ErrNotFound`, and `ErrConflict` are deliberately
preserved for the current self-hosted migration. New external composition roots
should import `application/port` directly; the aliases do not create a second
port contract.

## Operational transactions and analytics

`application/port.UnitOfWork` is the consistency boundary for configuration,
scheduling, leases, result idempotency, health, Incidents, IncidentEvents,
audit, and notification outbox rows. Consistency-sensitive writes stay in one
`Transact` callback.

ClickHouse or another OLAP database is not an operational-store replacement.
A future analytics implementation will use a separate archive/analytics port
fed asynchronously from committed operational events. An analytics or archive
interface must be a distinct declaration: it cannot alias, embed, accept, or
return `UnitOfWork`, `Repositories`, `Store`, or the operational repository
interfaces. The architecture test enforces that rule for every future public
port declaration whose name includes `Analytics` or `Archive`.

## Compatibility verification

Architecture tests reject forbidden dependency directions and discarded
incoming contexts. `integration/testdata/external-module` is a separate Go
module that imports all four public package roots, implements a UnitOfWork, and
constructs a service. Its harness runs with `GOWORK=off`, so Go's actual
`internal` visibility rules are exercised.

The OpenAPI document and generated SDK remain public contracts. Exported Go and
HTTP compatibility follow semantic-versioning discipline. Release metadata
must not describe Xisnove as Apache 2.0 licensed until the standard license and
required notice material are committed and checked.
