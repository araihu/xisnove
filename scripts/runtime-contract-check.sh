#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output_dir="$(mktemp -d)"
trap 'rm -rf "${output_dir}"' EXIT

version="0.0.0-m6.1"
commit="0123456789abcdef0123456789abcdef01234567"
build_date="2026-07-27T00:00:00Z"

build_and_check() {
  module_dir="$1"
  package="$2"
  binary="$3"
  buildinfo_package="$4"
  output_path="${output_dir}/${binary}"
  ldflags="-X ${buildinfo_package}.Version=${version} -X ${buildinfo_package}.Commit=${commit} -X ${buildinfo_package}.BuildDate=${build_date} -X ${buildinfo_package}.Dirty=false"

  (
    cd "${repo_root}/${module_dir}"
    GOWORK=off go build -trimpath -ldflags "${ldflags}" -o "${output_path}" "${package}"
  )

  actual="$(${output_path} --version)"
  expected="${binary} version=${version} commit=${commit} build_date=${build_date} dirty=false"
  if [[ "${actual}" != "${expected}" ]]; then
    printf 'version contract mismatch for %s\nwant: %s\n got: %s\n' "${binary}" "${expected}" "${actual}" >&2
    return 1
  fi
}

build_and_check "." "./cmd/xisnove-server" "xisnove-server" "github.com/araihu/xisnove/internal/buildinfo"
build_and_check "agent" "./cmd/xisnove-agent" "xisnove-agent" "github.com/araihu/xisnove/agent/internal/buildinfo"
build_and_check "cli" "./cmd/xisnove" "xisnove" "github.com/araihu/xisnove/cli/internal/buildinfo"
build_and_check "operator" "./cmd/xisnove-operator" "xisnove-operator" "github.com/araihu/xisnove/operator/internal/buildinfo"
build_and_check "ui" "./cmd/server" "xisnove-ui" "github.com/araihu/xisnove/ui/internal/buildinfo"
