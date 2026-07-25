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

## Operational transactions and analytics

`application/port.UnitOfWork` is the consistency boundary for configuration,
scheduling, leases, result idempotency, health, Incidents, IncidentEvents,
audit, and notification outbox rows. Consistency-sensitive writes stay in one
`Transact` callback.

ClickHouse or another OLAP database is not an operational-store replacement.
A future analytics implementation will use a separate archive/analytics port
fed asynchronously from committed operational events.

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
