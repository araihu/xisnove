# Release bundle

Every candidate commit produces immutable, SHA-addressed release bytes before
publication. The candidate contains binary archives, Helm charts, OCI layouts,
three deterministic bundles, SPDX JSON SBOMs, a deterministic license inventory,
a canonical candidate manifest, and its detached SHA-256 checksum.

Build one candidate from an exact clean commit with the locked toolchain:

```sh
XISNOVE_RELEASE_VERSION=1.2.3 \
XISNOVE_RELEASE_COMMIT="$(git rev-parse HEAD)" \
SOURCE_DATE_EPOCH="$(git show -s --format=%ct HEAD)" \
  make distribution-release-candidate
```

`make distribution-release-check` creates two detached clean worktrees, builds
the candidate independently in each, and compares the canonical manifest plus
the complete file, mode, size, and digest inventory. Buildx must exactly match
the pinned release-toolchain version.

`xisnove-source_<version>.tar.gz` contains the clean source tree plus root
`LICENSE` and `NOTICE`. It excludes Git state, worktrees, local artifacts, and
release output. `xisnove-deployment_<version>.tar.gz` contains the two Helm
charts, Compose, raw and systemd deployment resources, operator CRDs, this
project's legal files, and the upgrade runbook. Tar ownership is `0:0`; modes,
path order, gzip headers, and modification times derive from
`SOURCE_DATE_EPOCH`.

`xisnove-corresponding-sources_<version>.tar.gz` contains the exact Ubuntu
source package files and Go module zip required by every
`provide-corresponding-source-reference` obligation. Its committed lock binds
each affected PURL to a source identity and each downloaded byte sequence to
an HTTPS URL, size, and SHA-256 digest. Candidate construction fails before
publication on a missing mapping, changed size, changed digest, or partial
download.

## Candidate manifest

The manifest identity fixes repository, full commit SHA, version without the
`v` prefix, and positive `SOURCE_DATE_EPOCH`. Subjects are ordered by kind,
name, and platform. The current closure has exactly 65 subjects: 14 binary
archives, two charts, three bundles, four image indexes, eight platform
manifests, two chart OCI manifests, 30 SPDX documents, and two metadata files.
Each subject records its relative locator, byte size, and SHA-256 digest. The
manifest never lists itself; its adjacent `.sha256` file authenticates the
exact canonical JSON bytes.

Generate the subject plan from already-built candidate artifacts. The plan is
an array of `{kind,name,locator,path,platform?,mediaType?}` records. `path` and
`locator` must be the same clean relative path below the candidate directory;
`path` is consumed during generation and is not emitted. OCI subjects point to
the local index or manifest bytes, so offline verification never needs a
registry. Then run:

```sh
releasebundle manifest \
  --root dist/candidate \
  --repository github.com/araihu/xisnove \
  --commit "$GITHUB_SHA" \
  --version "$VERSION" \
  --source-date-epoch "$SOURCE_DATE_EPOCH" \
  --subjects dist/candidate/subjects.json \
  --output dist/candidate/candidate-manifest.json \
  --checksum dist/candidate/candidate-manifest.json.sha256
```

## SBOM and license closure

`generate-sboms.sh` invokes the checksum-verified Syft version from the release
toolchain lock. It normalizes the SPDX creation time and namespace from the
candidate epoch and subject digest. `inventory-licenses.sh` reads only those
SBOMs and the schema-v2 `build/release/licenses-policy.json`. Decisions are
scoped by provenance: ordinary artifacts use the default SPDX-expression
profile, Go modules use a stricter profile plus exact evidence-backed PURL
overrides, and Ubuntu runtime packages must match the complete
`ubuntu-runtime-lock.json` tuple of PURL, package verification code, reported
license, resolved license, evidence digest, and obligations. Global AGPL and
SSPL denials apply before and after any resolution. Every unknown, `NONE`,
`NOASSERTION`, unlocked package, changed expression, or evidence drift fails
the candidate.

`propose-ubuntu-lock` deterministically derives a review proposal from the
eight platform SBOMs. It does not broaden a profile: the committed lock is the
review boundary. Raw `NOASSERTION` values remain visible in the inventory and
are resolved only to a package-specific `LicenseRef-Ubuntu-<digest>` bound to
the exact package closure. Copyleft entries carry corresponding-source and
notice obligations. Every such inventory record names one source identity
from `build/release/corresponding-sources.lock.json`; that identity must cover
the exact package PURL. Exact Go exceptions retain the reported Syft
expression, resolved expression, obligations, source identity, and the
checksum-verified review record in `build/release/license-evidence/`.

All Ubuntu package installs use snapshot `20260701T000000Z`. The pinned Ubuntu
base does not contain a CA bundle, so each Dockerfile bootstraps the exact
`ca-certificates_20260601~22.04.1_all.deb` from that snapshot with Dockerfile
`ADD --checksum`, builds the initial trust bundle, and then lets APT verify the
snapshot's signed metadata and packages. Moving Ubuntu repositories and TLS
verification bypasses are forbidden.

## Clean consumer verification

Copy the candidate directory and the compiled `releasebundle` verifier into an
empty directory. Verification needs no repository checkout, Git metadata,
network, registry, or implicit relative input:

```sh
RELEASEBUNDLE_BIN="$PWD/releasebundle" ./verify-bundle.sh \
  --root "$PWD/candidate" \
  --manifest candidate-manifest.json \
  --checksum candidate-manifest.json.sha256
```

Publication promotes these accepted bytes and OCI digests. It never rebuilds.
Protected homelab acceptance copies the candidate OCI layouts into a disposable
digest-pinned registry, pulls the accepted linux/amd64 manifests, and runs the
existing kind journey with prebuilt images. Source-image rebuilds are forbidden
on this path.
