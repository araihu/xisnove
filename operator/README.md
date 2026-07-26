# Xisnove Kubernetes operator

This is an independently buildable Go module for the optional Kubernetes edge
surface. It owns only Kubernetes desired-state reconciliation and talks to the
Xisnove control plane through the narrow interface in
`internal/controlplane`. It never imports server internals, SQL packages, or
database adapters.

The manager uses the generated public Go SDK through the adapter in
`internal/controlplane/sdk`. It does not import server internals or access a
database. See [INTEGRATION.md](INTEGRATION.md) for the exact boundary.

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

## Runtime

The operator requires `XISNOVE_URL`,
`XISNOVE_PROVISIONING_CREDENTIAL_FILE`, `POD_NAMESPACE`, and a default Agent
image through `XISNOVE_AGENT_IMAGE`. The mounted credential file is reread for
every control-plane request so an external Secret materializer can rotate it
without restarting the manager. Requests use a dedicated HTTP client bounded by
`--request-timeout` or `XISNOVE_REQUEST_TIMEOUT` (`15s` by default).

Leader election uses a Lease in `POD_NAMESPACE` and is enabled by default.
The cache watches `XISNOVE_WATCH_NAMESPACES`, falling back to `POD_NAMESPACE`;
startup fails instead of silently watching every namespace when both are empty.
`--poll-interval`, `--heartbeat-stale-after`, and the per-Agent
`spec.discovery.staleAfterSeconds` configure observation freshness. Health and
readiness are served at `/healthz` and `/readyz`; readiness remains false until
the manager cache synchronizes. Manager shutdown requires a positive
`--graceful-shutdown-timeout`. In Helm, the pod termination grace period must
strictly exceed that timeout.

The Helm chart requires `controlPlane.existingSecret.name` and mounts only its
configured key. Agent credential Secrets are controller-owned and mutable. The
operator refuses to adopt an existing Secret not controlled by its Agent.
Discovery ServiceAccounts never receive Secret access.
