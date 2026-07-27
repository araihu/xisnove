#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH='' cd -- "$script_dir/../.." && pwd)
secret_dir=${XISNOVE_SECRET_DIR:-$script_dir/secrets}
# shellcheck source=deploy/compose/compose-command.sh
. "$script_dir/compose-command.sh"

control_plane_secret_owner=${XISNOVE_CONTROL_PLANE_SECRET_OWNER:-}
agent_credential_owner=${XISNOVE_AGENT_CREDENTIAL_OWNER:-}
if [ "$(id -u)" -eq 0 ]; then
	compose_host_owner=0:$(id -g)
	control_plane_secret_owner=${control_plane_secret_owner:-$compose_host_owner}
	agent_credential_owner=${agent_credential_owner:-$compose_host_owner}
fi

XISNOVE_SECRET_DIR=$secret_dir "$repo_dir/deploy/raw/prepare-secrets.sh"
export XISNOVE_SECRET_DIR="$secret_dir"

if [ "${XISNOVE_DATABASE_PROFILE:-sqlite}" = postgres ] && [ "${XISNOVE_COMPOSE_EXTERNAL_POSTGRES:-false}" != true ]; then
	compose -f "$script_dir/compose.yaml" --profile postgres up -d postgres
	attempt=0
	until compose -f "$script_dir/compose.yaml" exec -T postgres pg_isready -U xisnove -d xisnove >/dev/null 2>&1; do
		attempt=$((attempt + 1))
		[ "$attempt" -lt 30 ] || { printf 'PostgreSQL readiness timed out\n' >&2; exit 75; }
		sleep 1
	done
fi

migrate_service=migrate
bootstrap_service=bootstrap-admin
server_service=server
remote_profile=false
server_replicas=1
if [ "${XISNOVE_DATABASE_PROFILE:-sqlite}" = postgres ] || [ "${XISNOVE_DATABASE_PROFILE:-sqlite}" = turso-cloud ]; then
	migrate_service=migrate-remote
	bootstrap_service=bootstrap-admin-remote
	server_service=server-remote
	remote_profile=true
	server_replicas=${XISNOVE_SERVER_REPLICAS:-2}
	export XISNOVE_SERVER_HOST=server-remote
fi

compose -f "$script_dir/compose.yaml" run --rm --no-deps secret-init
compose -f "$script_dir/compose.yaml" run --rm --no-deps "$migrate_service"
compose -f "$script_dir/compose.yaml" run --rm --no-deps "$bootstrap_service"
if [ "$remote_profile" = true ]; then
	compose -f "$script_dir/compose.yaml" up -d --scale "$server_service=$server_replicas" "$server_service" ui
	published_endpoint=$(compose -f "$script_dir/compose.yaml" port --index 1 "$server_service" 8080 | sed -n '1p')
	[ -n "$published_endpoint" ] || { printf 'remote server published endpoint is unavailable\n' >&2; exit 75; }
	published_endpoint=$(normalize_compose_endpoint "$published_endpoint")
	bootstrap_api_url=${XISNOVE_API_URL:-http://$published_endpoint}
else
	compose -f "$script_dir/compose.yaml" up -d "$server_service" ui
	bootstrap_api_url=${XISNOVE_API_URL:-http://127.0.0.1:8080}
fi

XISNOVE_BOOTSTRAP_ONLINE=true \
XISNOVE_BOOTSTRAP_SKIP_OFFLINE=true \
XISNOVE_SECRET_DIR=$secret_dir \
XISNOVE_BOOTSTRAP_STATE_DIR=${XISNOVE_BOOTSTRAP_STATE_DIR:-$script_dir/.bootstrap-state} \
XISNOVE_SERVER_BIN=${XISNOVE_SERVER_BIN:-/usr/bin/false} \
XISNOVE_DATABASE_PROFILE=${XISNOVE_DATABASE_PROFILE:-sqlite} \
XISNOVE_DATABASE_URL=${XISNOVE_DATABASE_URL:-/var/lib/xisnove/xisnove.db} \
XISNOVE_API_URL=$bootstrap_api_url \
XISNOVE_CONTROL_PLANE_SECRET_OWNER=$control_plane_secret_owner \
XISNOVE_AGENT_CREDENTIAL_OWNER=$agent_credential_owner \
"$repo_dir/deploy/raw/bootstrap.sh"

compose -f "$script_dir/compose.yaml" run --rm secret-init
if [ "$remote_profile" = true ]; then
	compose -f "$script_dir/compose.yaml" up -d --scale "$server_service=$server_replicas" "$server_service" ui agent
else
	compose -f "$script_dir/compose.yaml" up -d "$server_service" ui agent
fi
