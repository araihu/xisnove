#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repository_root"

release_commit="${XISNOVE_RELEASE_COMMIT:-$(git rev-parse HEAD)}"
if [[ ! "$release_commit" =~ ^[0-9A-Fa-f]{40}$ ]]; then
  echo "XISNOVE_RELEASE_COMMIT must be a 40-character Git commit" >&2
  exit 2
fi

release_version="${XISNOVE_RELEASE_VERSION:-}"
if [[ -z "$release_version" ]]; then
  release_tag="$(git tag --points-at "$release_commit" --list 'v*' | LC_ALL=C sort | head -n 1)"
  release_version="${release_tag#v}"
fi
if [[ ! "$release_version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
  echo "release commit must have one semantic vX.Y.Z tag, or XISNOVE_RELEASE_VERSION must be set" >&2
  exit 2
fi

if [[ ! "${SOURCE_DATE_EPOCH:-}" =~ ^[0-9]+$ ]]; then
  echo "SOURCE_DATE_EPOCH must be a non-negative integer" >&2
  exit 2
fi
if build_date="$(date -u -r "$SOURCE_DATE_EPOCH" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null)"; then
  :
else
  build_date="$(date -u -d "@$SOURCE_DATE_EPOCH" '+%Y-%m-%dT%H:%M:%SZ')"
fi

goreleaser_bin="${GORELEASER_BIN:-}"
if [[ -z "$goreleaser_bin" ]]; then
  goreleaser_bin="$(command -v goreleaser || true)"
fi
if [[ -z "$goreleaser_bin" || ! -x "$goreleaser_bin" ]]; then
  echo "GoReleaser executable not found; set GORELEASER_BIN" >&2
  exit 2
fi

export XISNOVE_RELEASE_VERSION="$release_version"
export XISNOVE_RELEASE_COMMIT="$release_commit"
export XISNOVE_BUILD_DATE="$build_date"
export SOURCE_DATE_EPOCH
export XISNOVE_RELEASE_OUTPUT="${XISNOVE_RELEASE_OUTPUT:-dist}"

goreleaser_args=(release --snapshot --clean)

exec "$goreleaser_bin" "${goreleaser_args[@]}"
