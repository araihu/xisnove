# Distribution artifact matrix

`build/release/artifacts.json` is the machine-readable authority. Every row
uses `release.version` from the single `vX.Y.Z` tag.

| Artifact | Kind | Targets | Published location |
|---|---|---|---|
| `xisnove-server` | binary | Linux amd64/arm64, glibc 2.35+ | GitHub release |
| `xisnove-ui` | binary | Linux amd64/arm64 | GitHub release |
| `xisnove-agent` | binary | Linux amd64/arm64 | GitHub release |
| `xisnove-operator` | binary | Linux amd64/arm64 | GitHub release |
| `xisnove` | CLI binary | Linux, macOS, Windows amd64/arm64 | GitHub release |
| `xisnove-server` | OCI image | Linux amd64/arm64 | `ghcr.io/araihu/xisnove-server` |
| `xisnove-ui` | OCI image | Linux amd64/arm64 | `ghcr.io/araihu/xisnove-ui` |
| `xisnove-agent` | OCI image | Linux amd64/arm64 | `ghcr.io/araihu/xisnove-agent` |
| `xisnove-operator` | OCI image | Linux amd64/arm64 | `ghcr.io/araihu/xisnove-operator` |
| `xisnove` | Helm OCI chart | cluster-dependent | `oci://ghcr.io/araihu/charts/xisnove` |
| `xisnove-edge` | Helm OCI chart | cluster-dependent | `oci://ghcr.io/araihu/charts/xisnove-edge` |
| `xisnove-source` | source bundle | source closure | GitHub release |
| `xisnove-deployment` | deployment bundle | charts, Compose, raw, systemd, CRDs, upgrade docs | GitHub release |
| `xisnove-corresponding-sources` | corresponding-source bundle | exact Ubuntu and Go copyleft source bytes | GitHub release |

Each binary archive and chart package contains `LICENSE` and `NOTICE`.
Each image contains those files at `/usr/share/licenses/xisnove/`. Every
archive, chart, OCI index, and per-platform manifest receives a SHA-256 digest,
SBOM, provenance, and signature in the candidate digest manifest.

Schema v2 also freezes five metadata surfaces: detached SHA-256 checksums,
SPDX JSON SBOMs, a fail-closed canonical license inventory, the exact release
toolchain lock, and one canonical JSON digest manifest. The manifest closes
over archives, charts, all three bundles, OCI indexes, per-platform manifests,
SBOMs, and verified metadata without recursively naming its own digest. Its
detached checksum covers the manifest itself.

Cross-build success is insufficient. Native Linux amd64 and arm64 runners
execute each image. The server additionally runs local-Turso open, migrate,
and query on the glibc 2.35 baseline. macOS and Windows CLI targets execute
`--version` plus a bounded mock-server journey on native release runners.
