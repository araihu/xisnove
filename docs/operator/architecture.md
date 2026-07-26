# Kubernetes edge architecture

Xisnove treats Kubernetes as a desired-state client, never as the runtime
incident, result, lease, or notification-outbox database. The namespaced
`Monitor` and `Agent` CRDs contain requested configuration plus bounded current
status; all durable monitoring history remains in the relational control
plane.

## Ownership

The operator derives a stable remote owner key from API group, kind,
namespace, name, and Kubernetes UID. Reconciliation is an idempotent apply
through the public control-plane boundary. A recreated CR therefore cannot
take ownership of the previous CR's remote object.

`Monitor` owns only its one remote Monitor. `Agent` owns one remote Agent
identity, its namespaced credential Secret, and its Deployment. The Helm chart
owns the operator and discovery ServiceAccounts plus their RBAC. This keeps the
discovery workload's read-only identity separate from the operator identity
that can materialize its owned Secrets.

Both CRDs use finalizers. Remote deletion must prove ownership, and transient
control-plane failures retain the finalizer for retry. The
`monitoring.xisnove.io/force-delete: "true"` annotation is an explicit escape
hatch when the remote control plane is permanently unavailable; it abandons
the remote object rather than attempting an unsafe deletion.

## CRD surface

`Monitor.spec` carries supported HTTP, TCP, or DNS probe configuration and a
control-plane Location ID. `Monitor.status` contains observed generation,
external ID, current aggregate health, its transition time, and `Ready`,
`Synced`, and `Degraded` conditions. There is deliberately no Alert or Incident
CRD.

`Agent.spec` carries Location ID, capabilities, discovery scope, an
operator-owned Secret destination, explicit requested credential generation,
and workload settings. `Agent.status` contains only safe identifiers,
credential generation numbers, current rotation phase, last heartbeat and
discovery timestamps, and bounded conditions. Credential bytes never enter
status.

## Discovery

The discovery ServiceAccount can only `get`, `list`, and `watch` Services,
EndpointSlices, Ingresses, Gateways, HTTPRoutes, and GRPCRoutes. It has no
Secret permission. Gateway API objects are handled as unstructured optional
resources so clusters without those CRDs remain supported.

Observations normalize into stable candidate keys derived from the source UID,
protocol, and target. Only a successful complete snapshot may mark a missing
candidate stale. Staleness retains the catalog record and source provenance;
it does not delete a promoted Monitor. Promotion is always a separate explicit
control-plane action.

A complete snapshot with zero observations is valid and is the authoritative
way to mark every previously present candidate in scope absent. A partial
snapshot may carry `completedAt` for its observation window, but can never
infer absence. The Agent reports bounded structured API problems and never
copies response bodies into logs or status.

## Runtime state boundary

Kubernetes contains desired state, bounded conditions, operator-owned Agent
credentials, and Agent Deployments. Alert/Incident/result/notification state
remains relational; the operator creates neither operational CRDs nor Jobs.
The public HTTP/OpenAPI SDK is the sole production integration boundary. A
gRPC transport remains a possible later optimization and is not part of the
v1 edge contract.
