#!/bin/sh
set -eu

umask 077
secret_dir=${XISNOVE_SECRET_DIR:-/etc/xisnove/secrets}
mkdir -p "$secret_dir"
chmod 700 "$secret_dir"

random_base64() {
	openssl rand -base64 "$1" | tr -d '\n'
}

write_once() {
	name=$1
	value=$2
	path=$secret_dir/$name
	if [ ! -e "$path" ]; then
		temporary=$path.tmp.$$
		trap 'unlink "$temporary" 2>/dev/null || true' EXIT HUP INT TERM
		printf '%s\n' "$value" >"$temporary"
		chmod 600 "$temporary"
		if ln "$temporary" "$path" 2>/dev/null; then
			unlink "$temporary"
		else
			unlink "$temporary"
		fi
		trap - EXIT HUP INT TERM
	fi
	chmod 600 "$path"
	if [ "${XISNOVE_BOOTSTRAP_INTERRUPT_AFTER:-}" = "$name" ]; then
		printf 'interrupted after %s\n' "$name" >&2
		exit 75
	fi
}

write_once cursor-signing-key "$(random_base64 32)"
notification_key=$(random_base64 32)
write_once notification-keyring.json "{\"activeVersion\":1,\"keys\":[{\"version\":1,\"key\":\"$notification_key\"}]}"
write_once ui-cookie-secret "$(random_base64 32)"
if [ -n "${XISNOVE_TURSO_AUTH_TOKEN_FILE:-}" ]; then
	[ -f "$XISNOVE_TURSO_AUTH_TOKEN_FILE" ] || { printf 'managed Turso token file is unavailable\n' >&2; exit 2; }
	turso_auth_token=$(cat "$XISNOVE_TURSO_AUTH_TOKEN_FILE")
elif [ -n "${XISNOVE_TURSO_AUTH_TOKEN:-}" ]; then
	turso_auth_token=$XISNOVE_TURSO_AUTH_TOKEN
else
	turso_auth_token=CHANGE-ME-MANAGED-TURSO-TOKEN
fi
write_once turso-auth-token "$turso_auth_token"
if [ -n "${XISNOVE_DATABASE_URL_SOURCE_FILE:-}" ]; then
	[ -f "$XISNOVE_DATABASE_URL_SOURCE_FILE" ] || { printf 'database URL source file is unavailable\n' >&2; exit 2; }
	database_url=$(cat "$XISNOVE_DATABASE_URL_SOURCE_FILE")
else
	database_url=CHANGE-ME-POSTGRES-URL
fi
write_once database-url "$database_url"

if [ -e "$secret_dir/admin-password" ]; then
	admin_password=existing-value-is-preserved
elif [ -n "${XISNOVE_ADMIN_PASSWORD_FILE:-}" ]; then
	[ -f "$XISNOVE_ADMIN_PASSWORD_FILE" ] || { printf 'administrator password file is unavailable\n' >&2; exit 2; }
	admin_password=$(cat "$XISNOVE_ADMIN_PASSWORD_FILE")
elif [ -n "${XISNOVE_ADMIN_PASSWORD:-}" ]; then
	admin_password=$XISNOVE_ADMIN_PASSWORD
else
	admin_password=$(random_base64 24)
	printf 'generated administrator password at %s/admin-password\n' "$secret_dir" >&2
fi
write_once admin-password "$admin_password"

# Valid JSON placeholder lets Compose mount a stable file before one-time enrollment.
# bootstrap.sh atomically replaces it; it is never accepted by the control plane.
write_once agent-credential.json '{"credential":"CHANGE-ME-AFTER-ENROLLMENT","generation":1}'
