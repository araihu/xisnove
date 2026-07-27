# xisnove-edge

Installs the Xisnove Monitor and Agent CRDs, the namespaced operator RBAC, and
an optional read-only discovery Agent CR. The control plane is external to the
monitored cluster.

The chart never creates provisioning credentials. Set
`controlPlane.existingSecret.name` to an existing Secret and
`controlPlane.url` to the external control plane. Enable the default Agent only
after setting its control-plane Location ID:

```sh
helm upgrade --install edge ./charts/xisnove-edge \
  --namespace monitoring --create-namespace \
  --set controlPlane.url=https://xisnove.example.com \
  --set controlPlane.existingSecret.name=xisnove-operator \
  --set agent.enabled=true \
  --set agent.locationID=11111111-1111-1111-1111-111111111111
```

By default, operator and discovery access is limited to the release namespace.
Use `operator.watchNamespaces` and `agent.discovery.namespaces` for explicit
additional namespaces. `agent.discovery.clusterWide=true` creates the only
ClusterRoleBinding in the chart. Discovery RBAC never grants Secret access.

The existing Secret must contain the operator provisioning credential under
`controlPlane.existingSecret.key` (`token` by default). Only that key is mounted
read-only into the operator. Agent credential Secrets are separate, mutable,
and owned by their `Agent` resources; do not pre-create them. Vault Agent,
OpenBao Agent, CSI drivers, or External Secrets Operator may materialize the
operator provisioning Secret, but the v1 operator has no provider-specific
secret-manager integration.

Control-plane calls are bounded by `operator.requestTimeout` (`15s` by
default). Manager shutdown uses `operator.gracefulShutdownTimeoutSeconds`; the
pod's `operator.terminationGracePeriodSeconds` must be positive and strictly
larger so Kubernetes does not terminate the process before controller-runtime
finishes. The chart rejects more than one operator replica when leader election
is disabled.

Every Agent Deployment materialized by the operator exposes one named
`observability` container port on `9090`. Kubernetes liveness and readiness
probes call `/livez` and `/readyz` through that named port. The container binds
`0.0.0.0:9090` explicitly; raw Agent processes default to loopback. These
unauthenticated probe and Prometheus endpoints are an internal cluster surface,
not a public control API. On `SIGINT` or `SIGTERM`, readiness fails before lease
claims stop and the Agent receives a 15-second pod grace period for its bounded
10-second drain.

`values.schema.json` rejects YAML-coerced Secret names/keys, non-boolean leader
election values, fractional/zero replicas, and non-positive shutdown budgets
before templates render. Secret names and keys must contain a non-whitespace
character; use `--set-string` when supplying values that YAML could otherwise
coerce.
