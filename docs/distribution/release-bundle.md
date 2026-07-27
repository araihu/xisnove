# Release bundle

Every candidate commit produces immutable, SHA-addressed release bytes before
publication. The candidate contains binary archives, Helm charts, OCI layouts,
two deterministic bundles, SPDX JSON SBOMs, a deterministic license inventory,
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

## Candidate manifest

The manifest identity fixes repository, full commit SHA, version without the
`v` prefix, and positive `SOURCE_DATE_EPOCH`. Subjects are ordered by kind,
name, and platform. The current closure has exactly 64 subjects: 14 binary
archives, two charts, two bundles, four image indexes, eight platform
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
SBOMs and `build/release/licenses-policy.json`. Denied licenses and every
unknown, `NONE`, or `NOASSERTION` classification fail the candidate. Policy
expansion requires review; a missing classification is never silently allowed.

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
