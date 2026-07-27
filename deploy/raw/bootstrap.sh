#!/bin/sh
set -eu

umask 077
script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
secret_dir=${XISNOVE_SECRET_DIR:-/etc/xisnove/secrets}
state_dir=${XISNOVE_BOOTSTRAP_STATE_DIR:-/var/lib/xisnove/bootstrap}
server_bin=${XISNOVE_SERVER_BIN:-/usr/bin/xisnove-server}
api_url=${XISNOVE_API_URL:-http://127.0.0.1:8080}
admin_email=${XISNOVE_ADMIN_EMAIL:-admin@example.test}
agent_name=${XISNOVE_AGENT_NAME:-colocated-agent}
credential_file=${XISNOVE_AGENT_CREDENTIAL_FILE:-$secret_dir/agent-credential.json}
mkdir -p "$state_dir"
chmod 700 "$state_dir"

"$script_dir/prepare-secrets.sh"
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

boundary() {
	name=$1
	: >"$state_dir/$name.done"
	chmod 600 "$state_dir/$name.done"
	if [ "${XISNOVE_BOOTSTRAP_INTERRUPT_AFTER:-}" = "$name" ]; then
		printf 'interrupted after %s\n' "$name" >&2
		exit 75
	fi
}

if [ "${XISNOVE_BOOTSTRAP_SKIP_OFFLINE:-false}" != true ] && [ ! -f "$state_dir/migration.done" ]; then
	"$script_dir/migrate.sh"
	boundary migration
fi
if [ "${XISNOVE_BOOTSTRAP_SKIP_OFFLINE:-false}" != true ] && [ ! -f "$state_dir/administrator.done" ]; then
	"$server_bin" admin bootstrap "$@" --email "$admin_email" --password-file "$secret_dir/admin-password"
	boundary administrator
fi

if [ "${XISNOVE_BOOTSTRAP_ONLINE:-false}" != true ]; then
	exit 0
fi
command -v curl >/dev/null 2>&1 || { printf 'curl is required for online Agent bootstrap\n' >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { printf 'jq is required for online Agent bootstrap\n' >&2; exit 2; }

deadline=$(( $(date +%s) + ${XISNOVE_READY_TIMEOUT_SECONDS:-60} ))
until curl --fail --silent --show-error --max-time 2 "$api_url/readyz" >/dev/null; do
	[ "$(date +%s)" -lt "$deadline" ] || { printf 'control plane readiness timed out\n' >&2; exit 75; }
	sleep 1
done
boundary server-ready

password=$(cat "$secret_dir/admin-password")
session=$(jq -cn --arg email "$admin_email" --arg password "$password" '{email:$email,password:$password}' |
	curl --fail --silent --show-error --max-time 10 -H 'Content-Type: application/json' --data-binary @- "$api_url/v1/sessions" |
	jq -er '.token')

location_file=$state_dir/location.json
if [ ! -s "$location_file" ]; then
	temporary=$location_file.tmp.$$
	jq -cn --arg name "${XISNOVE_LOCATION_NAME:-colocated}" '{name:$name}' |
		curl --fail --silent --show-error --max-time 10 -H 'Content-Type: application/json' \
		-H "Authorization: Bearer $session" -H 'Idempotency-Key: raw-bootstrap-location-v1' \
		--data-binary @- "$api_url/v1/locations" >"$temporary"
	jq -e '.id' "$temporary" >/dev/null
	mv "$temporary" "$location_file"
	chmod 600 "$location_file"
fi
boundary location

if [ ! -e "$credential_file" ]; then
	mkdir -p "$(dirname -- "$credential_file")"
	temporary=$credential_file.tmp.$$
	printf '%s\n' '{"credential":"CHANGE-ME-AFTER-ENROLLMENT","generation":1}' >"$temporary"
	chmod 600 "$temporary"
	mv "$temporary" "$credential_file"
fi
if grep -q 'CHANGE-ME-AFTER-ENROLLMENT' "$credential_file"; then
	location_id=$(jq -er '.id' "$location_file")
	token_file=$state_dir/enrollment-token.json
	if [ ! -s "$token_file" ]; then
		temporary=$token_file.tmp.$$
		jq -cn --arg locationId "$location_id" '{locationId:$locationId,expiresInSeconds:900}' |
			curl --fail --silent --show-error --max-time 10 -H 'Content-Type: application/json' \
			-H "Authorization: Bearer $session" -H 'Idempotency-Key: raw-bootstrap-agent-token-v1' \
			--data-binary @- "$api_url/v1/agent-enrollment-tokens" >"$temporary"
		jq -e '.token' "$temporary" >/dev/null
		mv "$temporary" "$token_file"
		chmod 600 "$token_file"
	fi
	boundary enrollment-token

	enrollment_credential_file=$state_dir/agent-enrollment-credential
	if [ ! -s "$enrollment_credential_file" ]; then
		temporary=$enrollment_credential_file.tmp.$$
		openssl rand -base64 48 | tr -d '\n' >"$temporary"
		printf '\n' >>"$temporary"
		chmod 600 "$temporary"
		mv "$temporary" "$enrollment_credential_file"
	fi
	boundary enrollment-credential

	enrolled_file=$state_dir/enrolled-agent.json
	if [ ! -s "$enrolled_file" ]; then
		temporary=$enrolled_file.tmp.$$
		token=$(jq -er '.token' "$token_file")
		enrollment_credential=$(cat "$enrollment_credential_file")
		jq -cn --arg token "$token" --arg credential "$enrollment_credential" --arg name "$agent_name" \
			'{token:$token,credential:$credential,name:$name,capabilities:["http","tcp","dns"]}' |
			curl --fail --silent --show-error --max-time 10 -H 'Content-Type: application/json' \
			-H 'Idempotency-Key: raw-bootstrap-agent-enrollment-v1' \
			--data-binary @- "$api_url/v1/agent-enrollments" >"$temporary"
		jq -e '.credential and .credentialGeneration' "$temporary" >/dev/null
		mv "$temporary" "$enrolled_file"
		chmod 600 "$enrolled_file"
	fi
	jq -er '.agentId' "$enrolled_file" >"$state_dir/agent-id"
	chmod 600 "$state_dir/agent-id"
	boundary enrollment

	temporary=$credential_file.tmp.$$
	enrollment_credential=$(cat "$enrollment_credential_file")
	credential_generation=$(jq -er '.credentialGeneration' "$enrolled_file")
	jq -cn --arg credential "$enrollment_credential" --argjson generation "$credential_generation" \
		'{credential:$credential,generation:$generation}' >"$temporary"
	chmod 600 "$temporary"
	mv "$temporary" "$credential_file"
	if [ -n "${XISNOVE_AGENT_CREDENTIAL_OWNER:-}" ]; then
		chown "$XISNOVE_AGENT_CREDENTIAL_OWNER" "$credential_file"
	fi
	boundary credential
fi

if ! grep -q 'CHANGE-ME-AFTER-ENROLLMENT' "$credential_file" && [ -s "$state_dir/agent-id" ]; then
	for sensitive_state in enrollment-token.json agent-enrollment-credential enrolled-agent.json; do
		[ ! -e "$state_dir/$sensitive_state" ] || unlink "$state_dir/$sensitive_state"
	done
fi
