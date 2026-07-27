# Release versioning

Xisnove uses one release version for every binary, OCI image, and Helm chart.
The only release version source is an annotated Git tag shaped `vX.Y.Z` or
`vX.Y.Z-<identifier>`; its artifact value preserves the optional prerelease
suffix as `X.Y.Z[-<identifier>]` and its manifest reference is
`release.version`.
Module dependency versions are inputs, never competing product versions.

Releases follow semantic versioning. A major version may change supported Go,
OpenAPI, CLI, CRD, database, or deployment contracts. A minor version may add
backward-compatible behavior. A patch version fixes behavior without breaking
documented contracts.

Every binary receives identical `version`, 40-character `commit`, UTC
`build_date`, and `dirty=false` metadata. `--version` prints those fields and
exits zero without opening files, sockets, credentials, or a database. Release
builds reject a dirty tree, use `-trimpath`, and derive `build_date` from the
commit timestamp through `SOURCE_DATE_EPOCH`.

OCI artifacts receive both `X.Y.Z` and immutable `sha-<commit>` tags. The first
release workflow never publishes `latest`. Chart `version` and `appVersion`
both resolve from `release.version`. Archives use `xisnove-<artifact>_X.Y.Z_<os>_<arch>`;
Windows binaries carry `.exe`.

Pre-release tags follow `vX.Y.Z-<identifier>`. Candidate artifacts remain
non-public and SHA-addressed until authorized publication. A tag must point to
the accepted candidate commit; publication promotes the same digests or proves
byte equality after deterministic rebuild.
