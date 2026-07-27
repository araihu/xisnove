#!/bin/sh
set -eu

usage() {
  echo "usage: generate-sboms.sh --output-dir DIR --source-date-epoch EPOCH ARTIFACT..." >&2
  exit 2
}

output_dir=
source_date_epoch=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output-dir) output_dir=${2-}; shift 2 ;;
    --source-date-epoch) source_date_epoch=${2-}; shift 2 ;;
    --) shift; break ;;
    -*) usage ;;
    *) break ;;
  esac
done
[ -n "$output_dir" ] && [ -n "$source_date_epoch" ] && [ "$#" -gt 0 ] || usage

releasebundle=${RELEASEBUNDLE_BIN:-releasebundle}
syft=${SYFT_BIN:-syft}
mkdir -p "$output_dir"

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

for artifact in "$@"; do
  [ -f "$artifact" ] || { echo "missing SBOM subject: $artifact" >&2; exit 1; }
  base_name=$(basename "$artifact")
  name=$(printf '%s' "$base_name" | tr -c 'A-Za-z0-9._-' '_')
  raw="$output_dir/.${name}.spdx.json.raw"
  output="$output_dir/${name}.spdx.json"
  subject_digest=$(sha256_file "$artifact")
  "$syft" "file:$artifact" --enrich golang -o "spdx-json=$raw"
  "$releasebundle" normalize-sbom \
    --input "$raw" \
    --output "$output" \
    --subject-sha256 "$subject_digest" \
    --source-date-epoch "$source_date_epoch"
  rm -f "$raw"
done
