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

command=${XISNOVE_SERVER_BIN:-/usr/bin/xisnove-server}
if [ "${XISNOVE_DATABASE_PROFILE:-sqlite}" = sqlite ] || [ "${XISNOVE_DATABASE_PROFILE:-sqlite}" = turso-local ]; then
	exec "$script_dir/run-singleton.sh" "${XISNOVE_SINGLETON_LOCK:-/run/xisnove/server}" \
		"$command" serve "$@" --replicas 1 --installation-id "${XISNOVE_INSTALLATION_ID:-default}" \
		--listen "${XISNOVE_LISTEN:-127.0.0.1:8080}"
fi
exec "$command" serve "$@" --replicas "${XISNOVE_REPLICAS:-1}" \
	--installation-id "${XISNOVE_INSTALLATION_ID:-default}" --listen "${XISNOVE_LISTEN:-127.0.0.1:8080}"
