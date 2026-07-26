#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
operator_dir=$(CDPATH= cd -- "$script_dir/.." && pwd)
repository_dir=$(CDPATH= cd -- "$operator_dir/.." && pwd)
temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/xisnove-operator-codegen.XXXXXX")
trap 'rm -rf "$temporary_dir"' EXIT HUP INT TERM

cp "$operator_dir/api/v1alpha1/zz_generated.deepcopy.go" "$temporary_dir/zz_generated.deepcopy.go"
cd "$operator_dir"
GOWORK=off go tool controller-gen object paths=./api/...
cmp "$temporary_dir/zz_generated.deepcopy.go" "$operator_dir/api/v1alpha1/zz_generated.deepcopy.go"

mkdir -p "$temporary_dir/crds"
GOWORK=off go tool controller-gen crd:crdVersions=v1 paths=./api/... output:crd:dir="$temporary_dir/crds"
for name in monitoring.xisnove.io_agents.yaml monitoring.xisnove.io_monitors.yaml; do
	cmp "$temporary_dir/crds/$name" "$repository_dir/config/crd/bases/$name"
	cmp "$repository_dir/config/crd/bases/$name" "$repository_dir/charts/xisnove-edge/crds/$name"
done
