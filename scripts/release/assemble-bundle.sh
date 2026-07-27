#!/bin/sh
set -eu

usage() {
  echo "usage: assemble-bundle.sh --root DIR --output-dir DIR --version X.Y.Z --source-date-epoch EPOCH" >&2
  exit 2
}

root=
output_dir=
version=
source_date_epoch=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --root) root=${2-}; shift 2 ;;
    --output-dir) output_dir=${2-}; shift 2 ;;
    --version) version=${2-}; shift 2 ;;
    --source-date-epoch) source_date_epoch=${2-}; shift 2 ;;
    *) usage ;;
  esac
done
[ -n "$root" ] && [ -n "$output_dir" ] && [ -n "$version" ] && [ -n "$source_date_epoch" ] || usage

releasebundle=${RELEASEBUNDLE_BIN:-releasebundle}
mkdir -p "$output_dir"

"$releasebundle" bundle \
  --root "$root" \
  --output "$output_dir/xisnove-source_${version}.tar.gz" \
  --prefix "xisnove-source-${version}" \
  --source-date-epoch "$source_date_epoch" \
  --tracked-only \
  --include . \
  --exclude .git \
  --exclude .worktrees \
  --exclude .artifacts \
  --exclude dist

"$releasebundle" bundle \
  --root "$root" \
  --output "$output_dir/xisnove-deployment_${version}.tar.gz" \
  --prefix "xisnove-deployment-${version}" \
  --source-date-epoch "$source_date_epoch" \
  --tracked-only \
  --include LICENSE \
  --include NOTICE \
  --include charts/xisnove \
  --include charts/xisnove-edge \
  --include deploy/compose \
  --include deploy/raw \
  --include deploy/systemd \
  --include config/crd \
  --include docs/operations/upgrade.md
