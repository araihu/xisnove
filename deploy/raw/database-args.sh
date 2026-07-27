#!/bin/sh
# Sourced by deployment helpers. Produces newline-delimited arguments in DATABASE_ARGS.
set -eu

profile=${XISNOVE_DATABASE_PROFILE:-sqlite}
database_url=${XISNOVE_DATABASE_URL:-}
database_url_file=${XISNOVE_DATABASE_URL_FILE:-}
auth_token_file=${XISNOVE_DATABASE_AUTH_TOKEN_FILE:-}

if [ -n "$database_url" ] && [ -n "$database_url_file" ]; then
	printf 'set only one database URL source\n' >&2
	exit 2
fi
if [ -z "$database_url" ] && [ -z "$database_url_file" ]; then
	printf 'database URL or URL file is required\n' >&2
	exit 2
fi

DATABASE_ARGS="--database-profile
$profile"
if [ -n "$database_url_file" ]; then
	DATABASE_ARGS="$DATABASE_ARGS
--database-url-file
$database_url_file"
else
	DATABASE_ARGS="$DATABASE_ARGS
--database-url
$database_url"
fi
if [ -n "$auth_token_file" ]; then
	DATABASE_ARGS="$DATABASE_ARGS
--database-auth-token-file
$auth_token_file"
fi
export DATABASE_ARGS
