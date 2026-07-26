# Milestone 6: Distribution and Hardening

**Status:** frozen after two independent CLEAN reviews

**Base:** `codex/milestone-4a-control-plane` at `121d102`

## Goal

Ship one verifiable Xisnove release across Kubernetes, Docker Compose, and raw
hosts without collapsing the control-plane/monitored-infrastructure boundary.
The release contains four OCI images, five raw binaries, two Helm charts,
Compose and systemd resources, upgrade evidence, checksums, signatures, SBOMs,
and provenance.

This milestone does not add product features or move monitoring state into
Kubernetes. The control plane remains relationally backed and external to the
infrastructure it observes. The UI remains an SDK-only BFF.

## Frozen observations

- `charts/xisnove-edge` is the only production-shaped deployment artifact.
- The Dockerfiles under `integration/testdata/kind` are test fixtures, not
  production image definitions.
- `deploy/`, a control-plane chart, GoReleaser configuration, and release
  automation do not exist.
- Server, UI, Agent, and operator cross-build for Linux amd64/arm64. The CLI
  also cross-builds for macOS and Windows amd64/arm64.
- `xisnove-server` is dynamically linked even with `CGO_ENABLED=0` because the
  local-Turso path loads a bundled Rust ABI through purego. Raw server support
  therefore targets glibc 2.35 or newer (Ubuntu 22.04 LTS is the minimum raw
  host baseline). The release binary is built inside that baseline and must run
  its local-Turso open/migrate/query journey on native amd64 and arm64. Server
  images must not use or claim scratch/static portability.
- The repository has no `LICENSE` or `NOTICE`. The user-supplied Open Core
  architecture explicitly selects Apache 2.0 and requires the corresponding
  notices before the repository is presented as Apache-licensed.

## Release invariants

1. One `vX.Y.Z` tag supplies `X.Y.Z` to every binary, image, and chart.
2. OCI artifacts receive exact semver and immutable `sha-<commit>` tags. Do not
   publish `latest` in the first release workflow.
3. Release builds refuse a dirty tree, use `-trimpath`, inject stable build
   metadata, and derive timestamps from `SOURCE_DATE_EPOCH`.
4. Linux images support amd64 and arm64, use numeric non-root identities, carry
   CA certificates, tolerate a read-only root filesystem, and declare only
   explicit writable paths.
5. SQLite and local Turso permit one active server. PostgreSQL and managed
   Turso are replica-safe. Charts, Compose, and raw configuration reject unsafe
   combinations rather than documenting them as caveats.
6. Schema migration is explicit and bounded. Startup refuses an incompatible
   schema. A Helm migration Job may run only during install/upgrade; Jobs never
   execute probes or notification delivery.
7. Secrets are referenced, never rendered into test artifacts, command lines,
   logs, OCI labels, release archives, or uploaded failure evidence.
8. Every release artifact is checksummed, has an SBOM, and is covered by
   signature and provenance verification from a clean consumer environment.
9. The monitored cluster may disappear without removing control-plane history.
10. Publication and homelab acceptance remain credential-gated. Local and PR
    gates create no public release and mutate no live homelab resources.
11. A pre-tag candidate is an immutable, non-public bundle keyed by the exact
    commit SHA and a canonical signed digest manifest. Protected homelab tests
    consume only those digests. An authorized tag must point to that SHA, and
    publication promotes the same digests or reproduces and byte-compares them.

## Target matrix

| Artifact | Targets |
|---|---|
| `xisnove-server` | Linux amd64/arm64, tested glibc baseline |
| `xisnove-ui` | Linux amd64/arm64 |
| `xisnove-agent` | Linux amd64/arm64 |
| `xisnove-operator` | Linux amd64/arm64 |
| `xisnove` CLI | Linux/macOS/Windows amd64/arm64 |
| OCI images | server, UI, Agent, operator; Linux amd64/arm64 |
| Helm OCI | `xisnove`, `xisnove-edge` |

## Dependency graph

```text
M6.0 release contract
        |
M6.1 runtime hardening
        |
        +--------------------+--------------------+
        |                    |                    |
M6.2A production OCI   M6.2B control-plane Helm  M6.2C Compose/raw
        +--------------------+--------------------+
                             |
                   M6.3 release supply chain
                             |
                   M6.4 upgrade/homelab proof
                             |
                   M6.5 parent integration gate
```

M6.2A, M6.2B, and M6.2C may run in parallel only after M6.1 is frozen because
they consume the same health, version, process, port, and filesystem contracts.
Each writer owns disjoint paths. Shared Makefile, workspace, CI, and release
workflow edits remain parent-owned.

## M6.0 — Freeze the release contract

**Files**

- Create `docs/distribution/versioning.md`.
- Create `docs/distribution/compatibility.md`.
- Create `docs/distribution/artifact-matrix.md`.
- Create `docs/distribution/runtime-contracts.md` with one row per deployable:
  bind address, named ports, live/ready/metrics paths, authentication, timeout,
  response contract, readiness state machine, shutdown budget, writable paths,
  signals, and stable `--version` output/exit behavior.
- Create `docs/distribution/database-profile-matrix.md`, including singleton
  SQLite/local-Turso and replica-safe PostgreSQL/managed-Turso rules.
- Modify `README.md`, `Makefile`, `go.work`, and `.github/workflows/ci.yml` only
  in the parent integration session.
- Create the canonical Apache 2.0 `LICENSE` and a project `NOTICE`, then verify
  that source archives, binary archives, chart packages, and image metadata
  carry the required licensing material.
- Create a pinned tool/source manifest covering full GitHub Action SHAs, tool
  release checksums, builder/base-image digests, database service image digests,
  scanner database policy, and allowed time-bounded vulnerability exceptions.
- Give CI top-level `permissions: contents: read`, set
  `persist-credentials: false` on every checkout, and add a repository test that
  rejects mutable action, service-image, and production base-image references.

**Contract tests**

- A small manifest test asserts one version maps to every artifact name and
  target.
- `go work edit -json` includes every intended module, including `operator`, or
  the documentation records why an explicit standalone operator gate remains.
- Each module passes both its supported workspace and `GOWORK=off` mode.
- Release builds use the current checkout/workspace as the source closure for
  nested modules and assert that UI/operator embedded SDK and OpenAPI contract
  identities resolve to the release SHA. They never silently download older
  root pseudo-versions recorded in nested `go.mod` files.

**Gate**

```bash
git diff --check
GOWORK=off go test ./...
for module in agent cli operator ui; do (cd "$module" && GOWORK=off go test ./...); done
```

## M6.1 — Freeze runtime and build-information contracts

**Files**

- Modify `cmd/xisnove-server/**`, `ui/cmd/server/**`,
  `agent/cmd/xisnove-agent/**`, `operator/cmd/xisnove-operator/**`, and
  `cli/cmd/xisnove/**`.
- Add module-local build-information packages only where a shared public
  package would create an invalid dependency.
- Add Agent health/metrics code under `agent/internal/observability/**`.
- Modify `internal/adapters/sqlitecompat/migrate.go`,
  `internal/adapters/postgres/migrate.go`, and managed/local Turso migration
  code plus migration tests.
- Add process-version lease persistence under `db/migrations/**`, storage
  adapters, server lifecycle wiring, and `db migrate --phase=contract` tests.
- Regenerate operator CRDs and `charts/xisnove-edge/**` when Agent probes or
  ports change.

**Behavior**

- All five binaries expose the same version, commit, build date, and dirty
  policy through `--version` without starting dependencies.
- Every binary implements the exact process/port/filesystem contract frozen in
  `docs/distribution/runtime-contracts.md`; M6.2 consumers must not invent
  alternate probes or writable paths.
- UI exposes `/livez` and `/readyz` without weakening authentication on normal
  routes.
- Agent exposes bounded health and metrics listeners, graceful shutdown, and
  truthful readiness after credential/control-plane initialization.
- Operator-generated Agent Deployments wire probes and named ports.
- Readiness accepts an explicit supported schema interval instead of requiring
  only `LatestMigrationVersion`. Freeze an immutable N-1 binary/schema fixture
  at the M6.1 checkpoint; prove N-1 and N stay ready after an expand migration.
  Contract migrations are a separate phase. Every M6.1-or-newer server holds a
  database-backed process-version lease with bounded heartbeat and expiry.
  `db migrate --phase=contract` fails closed while any live lease belongs to a
  version that cannot read the contracted schema; stale leases expire and clean
  shutdown releases eagerly. Tests cover live/stale N-1 leases, clock bounds,
  crash recovery, and multiple installations sharing a remote database.
- `db migrate` has a bounded lock timeout, stable contention/timeout exit
  classes, and profile-specific serialization: PostgreSQL advisory lock,
  SQLite/local-Turso transaction/file ownership, and a database-backed
  CAS/lease for managed Turso. Concurrent migration processes either converge
  on the same version or one exits with the documented retryable contention
  result; they never interleave migrations.
- Freeze a per-deployment secret/reference matrix for the server cursor key,
  notification keyring, UI cookie secret, administrator password, and Agent
  credential. Bootstrap uses files or stdin, never secret command-line values.
  The first-install sequence is bounded and idempotent: migrate, bootstrap the
  administrator from a mounted password file, start the API, enroll the Agent,
  materialize its credential with owner-only permissions, then start the
  Agent. Retry and restart neither rotate nor leak an already-created secret.

**Gate**

```bash
make generated-check
make agent-check
make cli-check
make ui-check
make -C operator verify
make -C operator envtest
cd operator && GOWORK=off go test -race ./...
helm lint charts/xisnove-edge \
  --set controlPlane.url=https://example.test \
  --set controlPlane.existingSecret.name=test
make kind-edge-e2e
go test -race ./integration -run 'Migration|SchemaCompatibility' -count=1
```

## M6.2A — Production OCI images

**Owned files**

- Create `build/package/Dockerfile.server`.
- Create `build/package/Dockerfile.ui`.
- Create `build/package/Dockerfile.agent`.
- Create `build/package/Dockerfile.operator`.
- Create `docker-bake.hcl`.
- Modify `.dockerignore` without removing the `.env`, `.worktrees`, artifacts,
  or SDD exclusions proven in Milestone 5.
- Create `integration/distribution/images/**`.

**Tests first**

- Assert numeric UID/GID, OCI labels, CA bundle, entrypoint, and no secret in
  layers/history.
- Copy exact `LICENSE` and `NOTICE` files into every final image at
  `/usr/share/licenses/xisnove/` and verify their contents from each platform
  digest; OCI labels and SBOM metadata are additional evidence, not substitutes.
- Run each native architecture image with a read-only root filesystem.
- Exercise liveness/readiness and graceful termination.
- Give only the server its explicit data volume.
- Build the raw/server artifact inside glibc 2.35 and execute local-Turso
  open/migrate/query against Ubuntu 22.04 amd64 and arm64 runners/containers.
  Also test the other database modes used by packaging. Do not infer arm64
  runtime support from a successful cross-build or a `--version` command.

**Gate**

```bash
docker buildx bake --print
docker buildx bake test-amd64 --load
docker buildx bake test-arm64 --load
docker buildx bake oci-layout
go test ./integration/distribution/images -count=1
```

Matching native runners execute amd64 and arm64 test images; emulation is an
explicit supplemental gate, never the only arm64 evidence. The release target
exports a multi-platform OCI layout or disposable local-registry index,
inspects the index and both platform digests, executes each platform digest,
and scans SBOMs for both manifests and the index. A cache-only build is not
evidence.
Every image job performs `if: always()` cleanup and asserts no disposable
registry, buildx builder, mounted credential file, or temporary OCI layout
remains. Failure artifacts use an explicit redacted allowlist.

## M6.2B — Control-plane Helm chart

**Owned files**

- Create `charts/xisnove/**`.
- Create `integration/distribution/helm/**`.
- Create `docs/operations/kubernetes-control-plane.md`.

**Behavior**

- Deploy server and UI plus an optional colocated public Agent.
- SQLite creates one PVC and enforces one server replica.
- Local Turso, when exposed as a raw/single-pod profile, also enforces one active
  server. PostgreSQL and managed Turso use `existingSecret` references and
  permit multiple replicas.
- A bounded migration Job runs before compatible PostgreSQL/managed-Turso
  workloads on install/upgrade; SQLite follows the downtime path below.
- Ingress or Gateway API, TLS, NetworkPolicy, PDB, ServiceMonitor, resources,
  topology spread, affinity, and existing ServiceAccounts are optional typed
  values.
- No template renders a Secret value.
- Fresh-install values reference the complete secret matrix. Bootstrap and
  optional colocated-Agent enrollment use bounded Jobs and mounted files;
  retries are idempotent and never regenerate an existing credential.
- SQLite uses a one-replica StatefulSet with ordered replacement, not an online
  migration Job: terminate the old pod, confirm the singleton lease/file lock,
  attach the RWO PVC, run the bounded migration init container, then start the
  new server. A multi-node kind upgrade proves no concurrent mount or server.
  PostgreSQL and managed Turso retain the online expand-migration Job path.

**Gate**

```bash
helm lint charts/xisnove
helm template xisnove charts/xisnove --values integration/distribution/helm/sqlite-values.yaml
helm template xisnove charts/xisnove --values integration/distribution/helm/postgres-values.yaml
helm template xisnove charts/xisnove --values integration/distribution/helm/turso-managed-values.yaml
go test ./integration/distribution/helm -count=1
```

Tests include invalid values, SQLite replica refusal, migration hook ordering,
probe/port agreement, the complete `existingSecret` key matrix, Secret
redaction, managed-Turso multi-replica rendering, protected real managed-Turso
release smoke, and old/new image compatibility during expand/migrate/contract
upgrades.

## M6.2C — Compose and raw deployments

**Owned files**

- Create `deploy/compose/**`, `deploy/raw/**`, and `deploy/systemd/**`.
- Create `integration/distribution/deploy/**`.
- Create `docs/operations/compose.md` and
  `docs/operations/raw-deployment.md`.

**Behavior**

- Default Compose starts server, UI, and colocated Agent with SQLite.
- A PostgreSQL profile adds a disposable or external PostgreSQL configuration.
- A raw local-Turso profile exercises the supported embedded runtime and refuses
  multiple active server units; Compose does not advertise local Turso as a
  multi-replica profile.
- A separate example enrolls a remote outbound Agent.
- Managed Turso uses environment or secret files and is never emulated.
- systemd provides sysusers/tmpfiles, a bounded migration unit, hardened
  service units, restart policy, and explicit writable paths.
- Bootstrap helpers create or consume the complete secret matrix with owner-only
  permissions, use stdin/files for values, and recover idempotently after each
  process boundary.
- Example files contain names/placeholders, never credentials.

**Gate**

```bash
docker compose -f deploy/compose/compose.yaml config
docker compose -f deploy/compose/compose.yaml --profile postgres config
systemd-analyze verify deploy/systemd/*.service
shellcheck deploy/raw/*.sh scripts/distribution-*.sh
go test ./integration/distribution/deploy -count=1
```

The E2E test proves migration, login/readiness, Agent observation, SQLite
persistence across restart, PostgreSQL profile startup, and exact cleanup. It
interrupts after every bootstrap boundary and verifies stable administrator and
Agent identities plus byte-identical secret files. It starts a second
SQLite/local-Turso server and requires deterministic refusal without database
damage before accepting the singleton claim.

## M6.3 — Release and supply-chain automation

**Files**

- Create `.goreleaser.yaml`.
- Create `.github/workflows/release.yml`.
- Create `scripts/release/**`.
- Create `integration/distribution/release/**`.
- Create `docs/distribution/release-bundle.md` and
  `docs/operations/upgrade.md`.
- Add `.github/workflows/homelab-acceptance.yml` only as a protected/manual
  consumer of immutable artifacts.

**Pipeline**

1. Reuse full CI, kind, and managed-Turso gates for the exact candidate SHA.
2. In two isolated clean checkouts/containers of that SHA, use an identical
   pinned toolchain, commit-derived `SOURCE_DATE_EPOCH`, stable snapshot version,
   archive ownership/modes, and source closure. Compare canonical SHA-256
   manifests rather than snapshot filenames.
3. Build multiarch images into OCI layouts or a disposable local registry,
   inspect and execute every platform digest, and scan them before publication.
4. Package both charts plus Compose/raw/systemd/config/CRD/upgrade resources.
5. Produce SHA-256 checksums and per-archive/chart/OCI-index/platform SBOMs.
   Inventory dependency licenses, fail unknown or denied licenses, assemble
   required notices, and prove `LICENSE`/`NOTICE` exist inside every archive and
   chart and are represented by OCI labels and SBOM metadata.
6. Create a canonical digest manifest naming every archive, chart, SBOM, OCI
   index, and per-platform manifest. A distinct protected candidate-attestor
   job with only `contents: read`, `id-token: write`, and `attestations: write`
   uses `cosign sign-blob` and GitHub attestations, then stores the candidate in
   a non-public, fixed-retention artifact store keyed by SHA. Its policy binds
   the pre-tag workflow/ref and expected candidate SHA.
7. Verify the candidate from a clean consumer directory, then run the protected
   homelab workflow against only the signed manifest/digests.
8. After required-reviewer Environment approval and an authorized `vX.Y.Z` tag
   pointing to the candidate SHA, publish the same digests to GHCR/Helm OCI and
   create the GitHub release. The publication job separately runs `cosign sign`
   and attaches registry-native attestations to every image and chart reference
   by digest. Only this job has package/content write permissions.
9. Verify the candidate manifest bundle plus registry-native signatures,
   attestations, and release subjects. If artifacts are rebuilt rather than
   promoted, byte/digest equality with the accepted candidate is required.

Actions and tools are pinned. Unprivileged candidate jobs use `contents: read`
only; the candidate-attestor has only the three permissions stated above. The
protected publication job receives the minimum of `contents: write`, `packages:
write`, and `id-token: write`; a registry-attestation step/job receives
`attestations: write` only if actually required. No other job receives them.
Verification constrains issuer
`https://token.actions.githubusercontent.com`, exact workflow identity
`https://github.com/araihu/xisnove/.github/workflows/release.yml@refs/tags/vX.Y.Z`,
repository, ref, SHA, and Rekor inclusion. Candidate verification separately
constrains the pre-tag workflow identity/ref/SHA. A clean consumer runs both
`gh attestation verify --repo araihu/xisnove` and constrained `cosign verify`
against every subject in the digest manifest.

All M6.3 jobs use `if: always()` cleanup and residue assertions for disposable
registries, builders, mounted credential files, temporary consumer checkouts,
candidate staging directories, and redacted failure artifacts.

**Local gate**

```bash
goreleaser release --snapshot --clean
go test ./integration/distribution/release -count=1
```

## M6.4 — Upgrade and homelab acceptance

**Files**

- Create `integration/distribution/upgrade/**`.
- Create `scripts/distribution-upgrade-*` and
  `scripts/homelab-acceptance-*`.
- Create `docs/operations/homelab-acceptance.md`.
- Extend `docs/operations/upgrade.md` with rollback limits and database-profile
  procedures.

**Upgrade proof**

- Restore the immutable N-1 binary/schema fixture frozen in M6.1, run an expand
  migration, keep N-1 and N ready concurrently, remove N-1, run the contract
  phase, and prove the retired binary is then rejected. Preserve monitors,
  observations, incidents, outbox state, discovery links, credentials, and
  uptime aggregates.
- Drill Helm SQLite and PostgreSQL upgrades, Compose persistence/restart, and
  raw/systemd migration/restart.
- Assert a failed or incompatible migration never starts the application.

**Protected homelab proof**

- Observe pre-existing public Cloudflare HTTP and DNS names without requiring
  a Cloudflare mutation token.
- Observe a private/Tailscale name or service from an outbound Agent.
- Cover a physical or VPS target.
- Discover Kubernetes Service/Ingress/Gateway candidates and explicitly promote
  one.
- Deliver and resolve one Alertmanager notification.
- Partition or remove monitored infrastructure while the external control
  plane retains history.

The workflow uses a required-reviewer GitHub Environment, exact candidate SHA
and digest inputs, concurrency limits, bounded timeouts, no PR-authored code or
artifacts, and `if: always()` cleanup/redaction. Use dedicated scoped Xisnove
tokens and disposable resource names. Never upload
`.env`, Turso Platform keys, kubeconfig, Tailscale credentials, Cloudflare
tokens, notification URLs, database URLs/passwords, or rendered Secrets.

## M6.5 — Parent integration and publication gate

The parent integrates the three M6.2 nodes in a clean order, resolves shared
files, and runs:

```bash
make check
make -C operator verify
make -C operator envtest
make kind-edge-e2e
make distribution-check
helm lint charts/xisnove
helm lint charts/xisnove-edge \
  --set controlPlane.url=https://example.test \
  --set controlPlane.existingSecret.name=test
goreleaser release --snapshot --clean
```

Managed Turso, upgrade, supply-chain verification, and protected homelab
acceptance must pass against the exact non-public candidate before a v1 tag.
Creating the tag, publishing OCI artifacts, or releasing to GitHub remains a
separate explicitly authorized action after Environment approval.

## Review checklist

- Does every deployable have one version and truthful probes?
- Does every architecture run, rather than merely cross-compile?
- Is the server glibc requirement explicit and tested?
- Can SQLite ever render more than one replica?
- Do old and new images overlap safely during migration?
- Can any Secret value enter rendered YAML, a layer, logs, failure artifacts,
  or release archives?
- Can a clean consumer verify checksums, signatures, SBOMs, and provenance?
- Are release and homelab credentials absent from PR jobs?
- Do every published source/binary/chart/image artifact and its metadata carry
  the selected Apache 2.0 license and required notices?
- Does loss of monitored infrastructure preserve external history?

## Review record

Two independent read-only reviews challenged the frozen draft before any M6
implementation. The architecture review required and then approved:

- schema-range readiness plus an immutable N-1 expand/contract fixture;
- profile-specific serialized migrations and database process-version leases;
- explicit bootstrap/secret recovery and SQLite/local-Turso singleton gates;
- glibc 2.35 native local-Turso execution;
- managed-Turso Helm evidence and a SQLite downtime upgrade path.

The supply-chain review required and then approved:

- a signed, non-public, SHA-addressed candidate before tag/publication;
- separate candidate-attestor and publication permissions/trust identities;
- registry-native signatures after OCI publication;
- native per-platform execution, deterministic source closure, pinned inputs,
  license closure, and exact cleanup/residue assertions.

Both final re-reviews returned CLEAN with no P0-P3 findings.
