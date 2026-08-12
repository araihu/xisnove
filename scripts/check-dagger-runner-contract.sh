#!/usr/bin/env bash
set -euo pipefail

workflows=(
  .github/workflows/ci.yml
  .github/workflows/deploy-x9-site.yml
  .github/workflows/turso-conformance.yml
)
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
materializer="$repo_root/scripts/materialize-dagger-input.sh"

for workflow in "${workflows[@]}"; do
  if grep -nE "[\"']hostinger-vps[\"']" "$workflow"; then
    echo "legacy generic Hostinger runner label in $workflow" >&2
    exit 1
  fi

  while IFS=: read -r line _; do
    start=$((line > 8 ? line - 8 : 1))
    guard=$(sed -n "${start},${line}p" "$workflow")
    if ! grep -Fq "github.ref == 'refs/heads/main'" <<<"$guard" && \
      ! grep -Fq "startsWith(github.ref, 'refs/tags/')" <<<"$guard"; then
      echo "trusted runner without main-or-tag guard: $workflow:$line" >&2
      exit 1
    fi
  done < <(grep -nF 'hostinger-vps-trusted' "$workflow" || :)

  if ! grep -Fq 'DAGGER_REF: ${{ github.ref }}' "$workflow"; then
    echo "Dagger input does not receive the immutable GitHub ref: $workflow" >&2
    exit 1
  fi
done

tmp=$(mktemp -d "${TMPDIR:-/tmp}/xisnove-runner-contract.XXXXXX")
trap 'rm -rf -- "$tmp"' EXIT

assert_partition() {
  local mode=$1
  local event=$2
  local ref=$3
  local expected=$4
  local case_dir="$tmp/$mode-$event-${#ref}"

  mkdir -p "$case_dir"
  (
    cd "$case_dir"
    DAGGER_EVENT_NAME="$event" \
      DAGGER_REF="$ref" \
      DAGGER_RUN_ID=1 \
      DAGGER_RUN_ATTEMPT=1 \
      DAGGER_HAS_BASELINE=false \
      CLOUDFLARE_ACCOUNT_ID=contract-account \
      TURSO_GROUP=contract-group \
      bash "$materializer" "$mode" .dagger-inputs/input.json
    if ! grep -Fq "\"cache_partition\":\"$expected\"" .dagger-inputs/input.json; then
      echo "unexpected cache partition for $mode $event $ref" >&2
      exit 1
    fi
  )
}

assert_partition ci push refs/heads/master local
assert_partition ci push refs/heads/main trusted-main
assert_partition ci pull_request refs/pull/1/merge untrusted-pr
assert_partition site push refs/heads/codex/milestone-2b-persistence local
assert_partition site workflow_dispatch refs/heads/main trusted-site
assert_partition deploy push refs/heads/codex/milestone-2b-persistence local
assert_partition deploy workflow_dispatch refs/heads/main trusted-site
assert_partition turso schedule refs/heads/codex/milestone-2b-persistence local
assert_partition turso release refs/tags/v1.2.3 trusted-turso
