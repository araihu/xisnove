#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  echo "usage: accept-candidate-homelab.sh --candidate-root DIR --commit SHA --version X.Y.Z[-prerelease]" >&2
  exit 2
}

candidate_root=
commit=
version=
while [[ $# -gt 0 ]]; do
  case "$1" in
    --candidate-root) candidate_root=${2-}; shift 2 ;;
    --commit) commit=${2-}; shift 2 ;;
    --version) version=${2-}; shift 2 ;;
    *) usage ;;
  esac
done
[[ -n "$candidate_root" && -n "$commit" && -n "$version" ]] || usage
candidate_root=$(cd "$candidate_root" && pwd)
[[ "$commit" =~ ^[0-9a-f]{40}$ ]] || usage
[[ "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]] || usage

docker_bin=${DOCKER_BIN:-docker}
oras_bin=${ORAS_BIN:-oras}
kind_e2e_script=${XISNOVE_KIND_E2E_SCRIPT:-scripts/kind-edge-e2e.sh}
manifest="$candidate_root/candidate-manifest.json"
registry_image=${XISNOVE_ACCEPTANCE_REGISTRY_IMAGE:-docker.io/library/registry:2.8.3@sha256:a3d8aaa63ed8681a604f1dea0aa03f100d5895b6a58ace528858a7b332415373}
registry_name="xisnove-candidate-registry-$$"
registry_id=
declare -a local_images=()

for command in "$docker_bin" "$oras_bin" jq; do
  command -v "$command" >/dev/null 2>&1 || { echo "required command not found: $command" >&2; exit 2; }
done
[[ -f "$manifest" ]] || { echo "candidate manifest not found: $manifest" >&2; exit 1; }
jq -e --arg commit "$commit" --arg version "$version" \
  '.repository == "github.com/araihu/xisnove" and .commit == $commit and .version == $version' \
  "$manifest" >/dev/null

cleanup() {
  status=$?
  trap - EXIT INT TERM
  for image in "${local_images[@]}"; do "$docker_bin" image rm -f "$image" >/dev/null 2>&1 || true; done
  if [[ -n "$registry_id" ]]; then "$docker_bin" rm -f "$registry_id" >/dev/null 2>&1 || true; fi
  exit "$status"
}
trap cleanup EXIT INT TERM

registry_id="$($docker_bin run -d --name "$registry_name" -p 127.0.0.1::5000 "$registry_image")"
registry_address=
for _ in $(seq 1 30); do
  registry_address="$($docker_bin port "$registry_id" 5000/tcp 2>/dev/null | awk -F: 'NR == 1 {print "localhost:" $NF}')"
  [[ -n "$registry_address" ]] && break
done
[[ -n "$registry_address" ]] || { echo "ephemeral acceptance registry has no published port" >&2; exit 1; }

declare -A smoke_images=()
for name in xisnove-server xisnove-ui xisnove-agent xisnove-operator; do
  index_digest=$(jq -er --arg name "$name" '
    [.subjects[] | select(.kind == "oci-index" and .name == $name and (.platform // "") == "")] |
    if length == 1 then .[0].sha256 else error("expected one image index") end
  ' "$manifest")
  platform_digest=$(jq -er --arg name "$name" '
    [.subjects[] | select(.kind == "oci-platform-manifest" and .name == $name and .platform == "linux/amd64")] |
    if length == 1 then .[0].sha256 else error("expected one linux/amd64 manifest") end
  ' "$manifest")
  [[ "$index_digest" =~ ^[0-9a-f]{64}$ && "$platform_digest" =~ ^[0-9a-f]{64}$ ]] || {
    echo "invalid candidate digest for $name" >&2
    exit 1
  }
  layout="$candidate_root/oci/images/$name/layout"
  [[ -f "$layout/blobs/sha256/$index_digest" && -f "$layout/blobs/sha256/$platform_digest" ]] || {
    echo "candidate OCI layout closure missing for $name" >&2
    exit 1
  }
  remote="$registry_address/$name:$version"
  promoted=false
  for _ in $(seq 1 30); do
    if "$oras_bin" cp --from-oci-layout --to-plain-http "$layout@sha256:$index_digest" "$remote"; then
      promoted=true
      break
    fi
    sleep 1
  done
  [[ "$promoted" == true ]] || { echo "candidate promotion failed for $name" >&2; exit 1; }
  resolved=$(ORAS_EXPECTED_DIGEST="sha256:$index_digest" "$oras_bin" resolve --plain-http "$remote")
  [[ "$resolved" == "sha256:$index_digest" ]] || { echo "registry digest drift for $name" >&2; exit 1; }
  "$docker_bin" pull --platform linux/amd64 "$registry_address/$name@sha256:$index_digest" >/dev/null
  local_image="$name:accepted-${commit:0:12}"
  "$docker_bin" tag "$registry_address/$name@sha256:$index_digest" "$local_image"
  local_images+=("$local_image" "$registry_address/$name@sha256:$index_digest")
  smoke_images["$name"]="$local_image"
done

XISNOVE_KIND_E2E_PREBUILT=1 \
XISNOVE_KIND_E2E_SERVER_IMAGE="${smoke_images[xisnove-server]}" \
XISNOVE_KIND_E2E_UI_IMAGE="${smoke_images[xisnove-ui]}" \
XISNOVE_KIND_E2E_AGENT_IMAGE="${smoke_images[xisnove-agent]}" \
XISNOVE_KIND_E2E_OPERATOR_IMAGE="${smoke_images[xisnove-operator]}" \
  "$kind_e2e_script"
