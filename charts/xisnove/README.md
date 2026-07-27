# Xisnove control-plane chart

This chart deploys the relational Xisnove control plane: API server, UI BFF,
and an optional colocated public Agent. It does not deploy the Kubernetes edge
operator and never stores monitoring history in etcd.

The chart renders no `Secret`. Create the named Secrets before installation or
materialize them through ESO, Vault/OpenBao injection, or a CSI provider while
preserving the documented keys and mode `0440` for workload identity `101:101`.

Supported database profiles:

- `sqlite`: one StatefulSet replica and one RWO claim. Migration and admin
  bootstrap are ordered init containers; upgrades are downtime replacements.
- `postgres`: replica-safe Deployment and bounded pre-install/pre-upgrade
  expand-migration Job.
- `tursoManaged`: replica-safe Deployment and the same online migration model.

Local Turso is intentionally absent from this chart; v1 supports it only for
raw and Compose singleton deployments.

```bash
helm lint charts/xisnove
helm template xisnove charts/xisnove \
  --values integration/distribution/helm/postgres-values.yaml
```

See [Kubernetes control plane](../../docs/operations/kubernetes-control-plane.md)
for the Secret matrix, profile-specific install and upgrade procedures, and
optional Ingress/Gateway/NetworkPolicy/PDB/ServiceMonitor configuration.

The optional Agent defaults to an existing credential Secret. Set
`agent.enrollment.enabled=true` and reference a one-time enrollment-token
Secret to use the durable enrollment init flow. It stores only the generated
credential bundle and retry journal on the Agent's RWO claim; it never creates
a Kubernetes Secret or receives API RBAC.
