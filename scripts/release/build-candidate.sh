#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: build-candidate.sh --root DIR [--output-dir DIR] --version X.Y.Z[-prerelease] --commit SHA --source-date-epoch EPOCH" >&2
  exit 2
}

root=
output_dir=
version=
commit=
source_date_epoch=
while [[ $# -gt 0 ]]; do
  case "$1" in
    --root) root=${2-}; shift 2 ;;
    --output-dir) output_dir=${2-}; shift 2 ;;
    --version) version=${2-}; shift 2 ;;
    --commit) commit=${2-}; shift 2 ;;
    --source-date-epoch) source_date_epoch=${2-}; shift 2 ;;
    *) usage ;;
  esac
done
[[ -n "$root" && -n "$version" && -n "$commit" && -n "$source_date_epoch" ]] || usage

root=$(cd "$root" && pwd)
if [[ -z "$output_dir" ]]; then
  output_dir="$root/dist/candidate"
elif [[ "$output_dir" != /* ]]; then
  output_dir="$root/$output_dir"
fi
if [[ ! "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
  echo "version must be a semantic version without v prefix" >&2
  exit 2
fi
if [[ ! "$commit" =~ ^[0-9a-f]{40}$ ]]; then
  echo "commit must be 40 lowercase hexadecimal characters" >&2
  exit 2
fi
if [[ ! "$source_date_epoch" =~ ^[1-9][0-9]*$ ]]; then
  echo "source-date-epoch must be a positive Unix timestamp" >&2
  exit 2
fi

head_commit=$(git -C "$root" rev-parse HEAD)
if [[ "$head_commit" != "$commit" ]]; then
  echo "requested commit does not match HEAD" >&2
  exit 1
fi
if [[ -n "$(git -C "$root" status --porcelain=v1 --untracked-files=all)" ]]; then
  echo "working tree must be clean; untracked files including .env are candidate inputs risk" >&2
  exit 1
fi
if [[ -e "$output_dir" ]]; then
  echo "output directory already exists: $output_dir" >&2
  exit 1
fi

docker_bin=${DOCKER_BIN:-docker}
if ! command -v "$docker_bin" >/dev/null 2>&1; then
  echo "Docker executable not found: $docker_bin" >&2
  exit 2
fi

case "$(uname -m)" in
  x86_64|amd64) tool_arch=amd64 ;;
  arm64|aarch64) tool_arch=arm64 ;;
  *) echo "unsupported builder architecture: $(uname -m)" >&2; exit 2 ;;
esac

candidateplan=(go run ./scripts/release/cmd/candidateplan)
lock="$root/build/release/toolchain.lock.json"
lock_value() { (cd "$root" && "${candidateplan[@]}" lock --file "$lock" "$@"); }
go_version=$(lock_value --kind tools --name go --field version)
go_sha=$(lock_value --kind tools --name go --field checksum --platform "linux-$tool_arch")
goreleaser_version=$(lock_value --kind tools --name goreleaser --field version)
goreleaser_sha=$(lock_value --kind tools --name goreleaser --field checksum --platform "linux-$tool_arch")
syft_version=$(lock_value --kind tools --name syft --field version)
syft_sha=$(lock_value --kind tools --name syft --field checksum --platform "linux-$tool_arch")
oras_version=$(lock_value --kind tools --name oras --field version)
oras_sha=$(lock_value --kind tools --name oras --field checksum --platform "linux-$tool_arch")
buildx_version=$(lock_value --kind tools --name buildx --field version)
actual_buildx_version=$("$docker_bin" buildx version | awk '{print $2}' | sed 's/^v//')
if [[ "$actual_buildx_version" != "$buildx_version" ]]; then
  echo "Docker Buildx must match locked version $buildx_version" >&2
  exit 2
fi

release_tmp_root=${XISNOVE_RELEASE_TMPDIR:-$(dirname "$root")}
work_dir=$(mktemp -d "$release_tmp_root/xisnove-candidate.XXXXXXXX")
work_dir=$(cd "$work_dir" && pwd -P)
staging="$work_dir/candidate"
layouts="$work_dir/layouts"
mkdir -p "$staging" "$layouts"
chmod 0777 "$staging"
builder_image="xisnove-candidate-builder:${commit:0:12}-$tool_arch"
image_built=false
cleanup() {
  status=$?
  if [[ "$image_built" == true ]]; then "$docker_bin" run --rm -v "$staging:/out" "$builder_image" chown -R "$(id -u):$(id -g)" /out >/dev/null 2>&1 || true; fi
  if [[ "$image_built" == true ]]; then "$docker_bin" image rm -f "$builder_image" >/dev/null 2>&1 || true; fi
  rm -rf "$work_dir"
  exit "$status"
}
trap cleanup EXIT INT TERM

"$docker_bin" build \
  --platform "linux/$tool_arch" \
  --file "$root/build/release/Dockerfile.candidate-binaries" \
  --tag "$builder_image" \
  --build-arg "GO_VERSION=$go_version" \
  --build-arg "GO_SHA256=$go_sha" \
  --build-arg "GORELEASER_VERSION=$goreleaser_version" \
  --build-arg "GORELEASER_SHA256=$goreleaser_sha" \
  --build-arg "SYFT_VERSION=$syft_version" \
  --build-arg "SYFT_SHA256=$syft_sha" \
  --build-arg "ORAS_VERSION=$oras_version" \
  --build-arg "ORAS_SHA256=$oras_sha" \
  "$root"
image_built=true

if build_date=$(date -u -r "$source_date_epoch" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null); then :; else build_date=$(date -u -d "@$source_date_epoch" '+%Y-%m-%dT%H:%M:%SZ'); fi
run_container=("$docker_bin" run --rm -e HOME=/tmp -e GOCACHE=/tmp/go-build -e GOMODCACHE=/tmp/go-mod -e "XISNOVE_RELEASE_VERSION=$version" -e "XISNOVE_RELEASE_COMMIT=$commit" -e "XISNOVE_BUILD_DATE=$build_date" -e "SOURCE_DATE_EPOCH=$source_date_epoch" -v "$root:/src:ro" -v "$staging:/out" "$builder_image")

"${run_container[@]}" bash -euc '
  go build -trimpath -o /out/tools/releasebundle ./scripts/release/cmd/releasebundle
  go build -trimpath -o /out/tools/candidateplan ./scripts/release/cmd/candidateplan
  mkdir -p /out/archives /out/charts /out/bundles
  GORELEASER_BIN=/usr/local/bin/goreleaser XISNOVE_RELEASE_OUTPUT=/out/archives scripts/release/build-binaries.sh
  /out/tools/candidateplan package-chart --chart charts/xisnove --output "/out/charts/xisnove_${XISNOVE_RELEASE_VERSION}.tgz" --version "$XISNOVE_RELEASE_VERSION" --source-date-epoch "$SOURCE_DATE_EPOCH"
  /out/tools/candidateplan package-chart --chart charts/xisnove-edge --output "/out/charts/xisnove-edge_${XISNOVE_RELEASE_VERSION}.tgz" --version "$XISNOVE_RELEASE_VERSION" --source-date-epoch "$SOURCE_DATE_EPOCH"
  printf "{}" > /tmp/helm-config.json
  for chart in xisnove xisnove-edge; do
    layout="/out/oci/charts/${chart}/layout"
    mkdir -p "$layout"
    (cd /out/charts && oras push --oci-layout "${layout}:${XISNOVE_RELEASE_VERSION}" --annotation "org.opencontainers.image.created=${XISNOVE_BUILD_DATE}" --config /tmp/helm-config.json:application/vnd.cncf.helm.config.v1+json "${chart}_${XISNOVE_RELEASE_VERSION}.tgz:application/vnd.cncf.helm.chart.content.v1.tar+gzip")
    /out/tools/candidateplan verify-chart-layout --layout-dir "$layout" --chart "/out/charts/${chart}_${XISNOVE_RELEASE_VERSION}.tgz"
  done
  RELEASEBUNDLE_BIN=/out/tools/releasebundle scripts/release/assemble-bundle.sh --root /src --output-dir /out/bundles --version "$XISNOVE_RELEASE_VERSION" --source-date-epoch "$SOURCE_DATE_EPOCH"
'

export VERSION="$version" COMMIT="$commit" BUILD_DATE="$build_date"
(cd "$root" && "$docker_bin" buildx bake --allow="fs.write=$layouts" --file "$root/docker-bake.hcl" \
  --set "server-oci.output=type=oci,dest=$layouts/xisnove-server.tar" \
  --set "ui-oci.output=type=oci,dest=$layouts/xisnove-ui.tar" \
  --set "agent-oci.output=type=oci,dest=$layouts/xisnove-agent.tar" \
  --set "operator-oci.output=type=oci,dest=$layouts/xisnove-operator.tar" \
  server-oci ui-oci agent-oci operator-oci)

"$docker_bin" run --rm \
  -e HOME=/tmp -e "XISNOVE_RELEASE_VERSION=$version" -e "XISNOVE_RELEASE_COMMIT=$commit" -e "SOURCE_DATE_EPOCH=$source_date_epoch" \
  -v "$root:/src:ro" -v "$staging:/out" -v "$layouts:/layouts:ro" "$builder_image" bash -euc '
  for image in xisnove-server xisnove-ui xisnove-agent xisnove-operator; do
    /out/tools/candidateplan extract-oci --layout "/layouts/${image}.tar" --output-dir "/out/oci/images/${image}" --name "$image"
  done
  for chart in xisnove xisnove-edge; do
    /out/tools/candidateplan sbom-oci --layout-dir "/out/oci/charts/${chart}/layout" --kind oci-manifest --name "$chart" --output-dir /out/sboms --source-date-epoch "$SOURCE_DATE_EPOCH" --syft /usr/local/bin/syft --releasebundle /out/tools/releasebundle --platforms=false
  done
  mkdir -p /tmp/sbom-inputs
  set --
  while IFS= read -r artifact; do
    base=$(basename "$artifact" .tar.gz)
    cp "$artifact" "/tmp/sbom-inputs/archive--$base"
    set -- "$@" "/tmp/sbom-inputs/archive--$base"
  done < <(find /out/archives -type f -name "*.tar.gz" -print | LC_ALL=C sort)
  while IFS= read -r artifact; do
    base=$(basename "$artifact" "_${XISNOVE_RELEASE_VERSION}.tgz")
    cp "$artifact" "/tmp/sbom-inputs/chart--$base"
    set -- "$@" "/tmp/sbom-inputs/chart--$base"
  done < <(find /out/charts -type f -name "*.tgz" -print | LC_ALL=C sort)
  RELEASEBUNDLE_BIN=/out/tools/releasebundle SYFT_BIN=/usr/local/bin/syft scripts/release/generate-sboms.sh --output-dir /out/sboms --source-date-epoch "$SOURCE_DATE_EPOCH" "$@"
  for image in xisnove-server xisnove-ui xisnove-agent xisnove-operator; do
    /out/tools/candidateplan sbom-oci --layout-dir "/out/oci/images/${image}/layout" --kind oci-index --name "$image" --output-dir /out/sboms --source-date-epoch "$SOURCE_DATE_EPOCH" --syft /usr/local/bin/syft --releasebundle /out/tools/releasebundle
  done
  mkdir -p /out/metadata
  RELEASEBUNDLE_BIN=/out/tools/releasebundle scripts/release/inventory-licenses.sh --sbom-dir /out/sboms --policy /src/build/release/licenses-policy.json --output /out/metadata/licenses.json
  cp /src/build/release/toolchain.lock.json /out/metadata/toolchain.lock.json
  /out/tools/candidateplan plan --root /out --output /out/subjects.json --contract-version "$XISNOVE_RELEASE_VERSION"
  /out/tools/releasebundle manifest --root /out --repository github.com/araihu/xisnove --commit "$XISNOVE_RELEASE_COMMIT" --version "$XISNOVE_RELEASE_VERSION" --source-date-epoch "$SOURCE_DATE_EPOCH" --subjects /out/subjects.json --output /out/candidate-manifest.json --checksum /out/candidate-manifest.json.sha256
  /out/tools/candidateplan checksums --root /out --subjects /out/subjects.json --manifest candidate-manifest.json --output /out/checksums.txt
  /out/tools/releasebundle verify --root /out --manifest candidate-manifest.json --checksum candidate-manifest.json.sha256
'

"$docker_bin" run --rm -v "$staging:/out" "$builder_image" chown -R "$(id -u):$(id -g)" /out

mkdir -p "$(dirname "$output_dir")"
mv "$staging" "$output_dir"
printf 'release candidate: %s\n' "$output_dir"
