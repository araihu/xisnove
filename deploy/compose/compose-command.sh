#!/bin/sh
# Sourced by Compose helpers. Defines compose() without evaluating command text.
set -eu

compose_backend=${COMPOSE_COMMAND:-}
if [ -z "$compose_backend" ]; then
	if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
		compose_backend='docker compose'
	elif command -v docker-compose >/dev/null 2>&1; then
		compose_backend=docker-compose
	else
		printf 'Docker Compose plugin or docker-compose binary is required\n' >&2
		exit 2
	fi
fi

case "$compose_backend" in
	'docker compose')
		compose() { docker compose "$@"; }
		;;
	docker-compose)
		compose() { docker-compose "$@"; }
		;;
	*[!A-Za-z0-9_./-]*)
		printf 'COMPOSE_COMMAND must be docker compose, docker-compose, or one executable path\n' >&2
		exit 2
		;;
	*)
		command -v "$compose_backend" >/dev/null 2>&1 || {
			printf 'configured Compose executable is unavailable: %s\n' "$compose_backend" >&2
			exit 2
		}
		compose() { "$compose_backend" "$@"; }
		;;
esac

normalize_compose_endpoint() {
	endpoint=$1
	case "$endpoint" in
		127.0.0.1:*)
			printf '%s\n' "$endpoint"
			;;
		0.0.0.0:*|'[::]':*)
			printf '127.0.0.1:%s\n' "${endpoint##*:}"
			;;
		*:*)
			printf '%s\n' "$endpoint"
			;;
		*)
			printf 'invalid Compose published endpoint: %s\n' "$endpoint" >&2
			return 2
			;;
	esac
}
