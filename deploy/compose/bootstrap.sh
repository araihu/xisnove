#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH='' cd -- "$script_dir/../.." && pwd)
compose_command=${COMPOSE_COMMAND:-docker-compose}
secret_dir=${XISNOVE_SECRET_DIR:-$script_dir/secrets}

XISNOVE_SECRET_DIR=$secret_dir "$repo_dir/deploy/raw/prepare-secrets.sh"
export XISNOVE_SECRET_DIR="$secret_dir"

if [ "${XISNOVE_DATABASE_PROFILE:-sqlite}" = postgres ] && [ "${XISNOVE_COMPOSE_EXTERNAL_POSTGRES:-false}" != true ]; then
	"$compose_command" -f "$script_dir/compose.yaml" --profile postgres up -d postgres
	attempt=0
	until "$compose_command" -f "$script_dir/compose.yaml" exec -T postgres pg_isready -U xisnove -d xisnove >/dev/null 2>&1; do
		attempt=$((attempt + 1))
		[ "$attempt" -lt 30 ] || { printf 'PostgreSQL readiness timed out\n' >&2; exit 75; }
		sleep 1
	done
fi

migrate_service=migrate
bootstrap_service=bootstrap-admin
server_service=server
if [ "${XISNOVE_DATABASE_PROFILE:-sqlite}" = postgres ] || [ "${XISNOVE_DATABASE_PROFILE:-sqlite}" = turso-cloud ]; then
	migrate_service=migrate-remote
	bootstrap_service=bootstrap-admin-remote
	server_service=server-remote
	export XISNOVE_SERVER_HOST=server-remote
fi

"$compose_command" -f "$script_dir/compose.yaml" run --rm --no-deps secret-init
"$compose_command" -f "$script_dir/compose.yaml" run --rm --no-deps "$migrate_service"
"$compose_command" -f "$script_dir/compose.yaml" run --rm --no-deps "$bootstrap_service"
"$compose_command" -f "$script_dir/compose.yaml" up -d "$server_service" ui

XISNOVE_BOOTSTRAP_ONLINE=true \
XISNOVE_BOOTSTRAP_SKIP_OFFLINE=true \
XISNOVE_SECRET_DIR=$secret_dir \
XISNOVE_BOOTSTRAP_STATE_DIR=${XISNOVE_BOOTSTRAP_STATE_DIR:-$script_dir/.bootstrap-state} \
XISNOVE_SERVER_BIN=${XISNOVE_SERVER_BIN:-/usr/bin/false} \
XISNOVE_DATABASE_PROFILE=${XISNOVE_DATABASE_PROFILE:-sqlite} \
XISNOVE_DATABASE_URL=${XISNOVE_DATABASE_URL:-/var/lib/xisnove/xisnove.db} \
"$repo_dir/deploy/raw/bootstrap.sh"

"$compose_command" -f "$script_dir/compose.yaml" run --rm secret-init
"$compose_command" -f "$script_dir/compose.yaml" up -d "$server_service" ui agent
