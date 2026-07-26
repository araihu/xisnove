# Frozen control-plane contract integration

Status: blocked on API/mock task `019f9b9d-47d6-7b82-a556-6690a8ab9383` and
its frozen commit SHA. This branch intentionally defines no HTTP paths and no
copies of public API models.

When the frozen SHA is handed off, add one adapter under
`operator/internal/controlplane/sdk` that imports only the generated public
`github.com/araihu/xisnove/sdk` package and satisfies the existing
`controlplane.Client` interface. Do not import root `internal/`, `domain`,
`application`, persistence, or database packages.

## Required public contract behavior

### Monitor reconciliation

- Idempotent apply/upsert of the full supported Monitor configuration.
- A stable owner key plus Kubernetes UID is the idempotency and ownership key.
- Apply accepts an optional external ID and returns the canonical external ID,
  aggregate health, and health transition time.
- Delete proves the owner before mutation. It must support owner-only lookup
  when a remote apply committed but the Kubernetes status write was lost.
- Not-found deletion is success; an ownership mismatch is a distinct error.

### Agent identity and credentials

- Idempotent Agent apply/update keyed by the stable Kubernetes owner key.
- Initial registration can request a one-time credential and returns only the
  credential, generation, and remote Agent ID needed for Secret materialization.
- Explicit credential issuance accepts the requested generation and an
  idempotency key. Repeating it after a Secret-write crash returns the same
  active generation without prematurely invalidating the previous credential.
- Revocation is idempotent and scoped to one Agent and generation.
- Agent observation returns the last heartbeat credential generation, last
  heartbeat time, and last complete discovery snapshot time.
- Agent deletion supports the same owner-only recovery rule as Monitor
  deletion.

### Discovery catalog

- Upsert a complete snapshot of normalized candidates using stable source UID,
  namespace, name, protocol, target, labels, and network perspective.
- A complete snapshot marks previously seen missing candidates stale. A failed
  or partial watch/list must not mark candidates stale.
- Candidate cataloging has no implicit Monitor creation. Promotion is a
  separate explicit operation and deleting or staling a candidate never
  deletes a promoted Monitor.
- Gateway API resources must remain optional when their CRDs are absent.

### Authentication and testing

- The operator SDK client reads the narrowly scoped provisioning credential
  from the mounted file on each request or through an atomic reload-safe
  provider. No token may enter logs, events, metrics, or CRD status.
- Add SDK/mock contract tests for every mapping above, including lost status
  writes, replayed credential issuance, ownership conflicts, unavailable
  Gateway API CRDs, and candidate staleness.
- Wire `cmd/xisnove-operator` only after the adapter compiles against the frozen
  SDK SHA. Controller setup uses the manager's uncached `APIReader` for exact
  credential Secret reads so RBAC does not need namespace-wide Secret
  list/watch. Until then, the chart and controllers are contract-independent
  foundations and must not be described as deployable end-to-end.

## Agent runtime dependency

The edge chart and `Agent` controller configure the public Agent image with
`kubernetes-discovery`/`kubernetes-watch`, discovery namespaces/resources, and
a mounted credential file. The Agent track must consume the discovery
normalizers or provide equivalent behavior and publish complete snapshots
through the frozen SDK. This operator branch does not modify `agent/**`.
