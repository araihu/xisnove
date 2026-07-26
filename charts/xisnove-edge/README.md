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
