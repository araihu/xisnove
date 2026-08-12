#!/usr/bin/env bash
set -euo pipefail

mode=${1-}
output=${2-}
umask 077

if [[ $# -ne 2 ]]; then
  echo "usage: materialize-dagger-input.sh MODE .dagger-inputs/FILE.json" >&2
  exit 2
fi
if [[ ! "$output" =~ ^\.dagger-inputs/[A-Za-z0-9][A-Za-z0-9._-]*\.json$ ]]; then
  echo "target must be a .dagger-inputs/*.json file" >&2
  exit 2
fi

event=${DAGGER_EVENT_NAME-}
case "$event" in
  pull_request|push|workflow_dispatch|schedule|release|local) ;;
  *) echo "unsupported DAGGER_EVENT_NAME" >&2; exit 2 ;;
esac

ref=${DAGGER_REF-}
case "$event" in
  local) ;;
  pull_request)
    [[ "$ref" =~ ^refs/pull/[1-9][0-9]*/merge$ ]] || {
      echo "DAGGER_REF must be a GitHub pull request merge ref" >&2
      exit 2
    }
    ;;
  *)
    [[ "$ref" =~ ^refs/(heads|tags)/[^[:space:]]+$ ]] || {
      echo "DAGGER_REF must be a GitHub heads or tags ref" >&2
      exit 2
    }
    ;;
esac

run_id=${DAGGER_RUN_ID-}
run_attempt=${DAGGER_RUN_ATTEMPT-}
if [[ ! "$run_id" =~ ^[1-9][0-9]*$ || ! "$run_attempt" =~ ^[1-9][0-9]*$ ]]; then
  echo "invalid Dagger run nonce" >&2
  exit 2
fi
run_nonce="${run_id}-${run_attempt}"

safe_identifier() {
  [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9._:-]{0,254}$ ]]
}

is_main_ref() {
  [[ "$ref" == "refs/heads/main" ]]
}

is_tag_ref() {
  [[ "$ref" == refs/tags/* && "$ref" != "refs/tags/" ]]
}

if [[ -e .dagger-inputs || -L .dagger-inputs ]]; then
  if [[ ! -d .dagger-inputs || -L .dagger-inputs ]]; then
    echo ".dagger-inputs must be a real directory" >&2
    exit 2
  fi
else
  mkdir -m 700 .dagger-inputs
fi
if [[ -L "$output" || ( -e "$output" && ! -f "$output" ) ]]; then
  echo "Dagger input must be a regular file" >&2
  exit 2
fi

case "$mode" in
  ci)
    case ${DAGGER_HAS_BASELINE-} in
      true|false) ;;
      *) echo "DAGGER_HAS_BASELINE must be true or false" >&2; exit 2 ;;
    esac
    cache_partition=local
    if [[ "$event" == pull_request ]]; then
      cache_partition=untrusted-pr
    elif is_main_ref; then
      cache_partition=trusted-main
    fi
    printf '{"cache_partition":"%s","event_name":"%s","has_baseline":"%s","run_nonce":"%s"}\n' \
      "$cache_partition" "$event" "$DAGGER_HAS_BASELINE" "$run_nonce" > "$output"
    ;;
  site)
    cache_partition=local
    if [[ "$event" == pull_request ]]; then
      cache_partition=untrusted-pr
    elif is_main_ref; then
      cache_partition=trusted-site
    fi
    printf '{"cache_partition":"%s","event_name":"%s","has_baseline":"false","run_nonce":"%s"}\n' \
      "$cache_partition" "$event" "$run_nonce" > "$output"
    ;;
  deploy)
    account_id=${CLOUDFLARE_ACCOUNT_ID-}
    if ! safe_identifier "$account_id"; then
      echo "CLOUDFLARE_ACCOUNT_ID is not a safe identifier" >&2
      exit 2
    fi
    cache_partition=local
    if is_main_ref; then
      cache_partition=trusted-site
    fi
    printf '{"account_id":"%s","cache_partition":"%s","effect_nonce":"%s"}\n' \
      "$account_id" "$cache_partition" "$run_nonce" > "$output"
    ;;
  turso)
    turso_group=${TURSO_GROUP-}
    turso_org=${TURSO_ORG-}
    if ! safe_identifier "$turso_group"; then
      echo "TURSO_GROUP is not a safe identifier" >&2
      exit 2
    fi
    if [[ -n "$turso_org" ]] && ! safe_identifier "$turso_org"; then
      echo "TURSO_ORG is not a safe identifier" >&2
      exit 2
    fi
    cache_partition=local
    if [[ "$event" != local ]] && { is_main_ref || is_tag_ref; }; then
      cache_partition=trusted-turso
    fi
    printf '{"cache_partition":"%s","run_nonce":"%s","turso_group":"%s","turso_org":"%s"}\n' \
      "$cache_partition" "$run_nonce" "$turso_group" "$turso_org" > "$output"
    ;;
  *)
    echo "unsupported Dagger input mode" >&2
    exit 2
    ;;
esac
