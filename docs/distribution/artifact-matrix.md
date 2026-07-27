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

Each binary archive and chart package contains `LICENSE` and `NOTICE`.
Each image contains those files at `/usr/share/licenses/xisnove/`. Every
archive, chart, OCI index, and per-platform manifest receives a SHA-256 digest,
SBOM, provenance, and signature in the candidate digest manifest.

Cross-build success is insufficient. Native Linux amd64 and arm64 runners
execute each image. The server additionally runs local-Turso open, migrate,
and query on the glibc 2.35 baseline. macOS and Windows CLI targets execute
`--version` plus a bounded mock-server journey on native release runners.
