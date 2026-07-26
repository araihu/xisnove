# Xisnove Kubernetes operator

This is an independently buildable Go module for the optional Kubernetes edge
surface. It owns only Kubernetes desired-state reconciliation and talks to the
Xisnove control plane through the narrow interface in
`internal/controlplane`. It never imports server internals, SQL packages, or
database adapters.

The generated public SDK adapter is intentionally gated on the frozen API/mock
contract. See [INTEGRATION.md](INTEGRATION.md) for the exact dependencies.

## Development

```sh
make generate
make verify
make test
```

`make generate` refreshes DeepCopy implementations, canonical CRDs under
`config/crd/bases`, and the chart's CRD copies. `make verify` fails on any
generation drift.

The controller tests use a fake Kubernetes client and a fake control-plane
boundary. The manifest tests structurally validate both CRDs and render the
Helm chart to enforce the discovery RBAC boundary.
