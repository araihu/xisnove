#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=deploy/raw/database-args.sh
. "$script_dir/database-args.sh"
set -f
old_ifs=$IFS
IFS='
'
# Newlines are the intentional argument boundary; globbing is disabled above.
# shellcheck disable=SC2086
set -- $DATABASE_ARGS
IFS=$old_ifs

timeout_command=${XISNOVE_TIMEOUT_COMMAND:-timeout}
exec "$timeout_command" "${XISNOVE_MIGRATION_TIMEOUT:-60s}" \
	"${XISNOVE_SERVER_BIN:-/usr/bin/xisnove-server}" db migrate \
	"$@" --installation-id "${XISNOVE_INSTALLATION_ID:-default}" \
	--lock-timeout "${XISNOVE_MIGRATION_LOCK_TIMEOUT:-30s}" --phase "${XISNOVE_MIGRATION_PHASE:-expand}"
