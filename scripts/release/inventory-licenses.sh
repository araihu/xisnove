#!/bin/sh
set -eu

usage() {
  echo "usage: inventory-licenses.sh --sbom-dir DIR --policy FILE --output FILE" >&2
  exit 2
}

sbom_dir=
policy=
output=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --sbom-dir) sbom_dir=${2-}; shift 2 ;;
    --policy) policy=${2-}; shift 2 ;;
    --output) output=${2-}; shift 2 ;;
    *) usage ;;
  esac
done
[ -n "$sbom_dir" ] && [ -n "$policy" ] && [ -n "$output" ] || usage

releasebundle=${RELEASEBUNDLE_BIN:-releasebundle}
set --
for sbom in "$sbom_dir"/*.spdx.json; do
  [ -f "$sbom" ] || { echo "no SPDX JSON SBOMs in $sbom_dir" >&2; exit 1; }
  set -- "$@" --sbom "$sbom"
done
"$releasebundle" licenses --policy "$policy" --output "$output" "$@"
