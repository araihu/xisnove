#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
operator_dir=$(CDPATH= cd -- "$script_dir/.." && pwd)
repository_dir=$(CDPATH= cd -- "$operator_dir/.." && pwd)

cd "$operator_dir"
GOWORK=off go tool controller-gen object paths=./api/...
GOWORK=off go tool controller-gen crd:crdVersions=v1 paths=./api/... output:crd:dir="$repository_dir/config/crd/bases"

mkdir -p "$repository_dir/charts/xisnove-edge/crds"
cp "$repository_dir/config/crd/bases/monitoring.xisnove.io_agents.yaml" "$repository_dir/charts/xisnove-edge/crds/monitoring.xisnove.io_agents.yaml"
cp "$repository_dir/config/crd/bases/monitoring.xisnove.io_monitors.yaml" "$repository_dir/charts/xisnove-edge/crds/monitoring.xisnove.io_monitors.yaml"
