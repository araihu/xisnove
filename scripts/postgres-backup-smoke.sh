#!/usr/bin/env bash
set -euo pipefail

: "${POSTGRES_HOST:?POSTGRES_HOST is required}"
: "${POSTGRES_PORT:?POSTGRES_PORT is required}"
: "${POSTGRES_USER:?POSTGRES_USER is required}"
: "${POSTGRES_PASSWORD:?POSTGRES_PASSWORD is required}"
: "${POSTGRES_DATABASE:?POSTGRES_DATABASE is required}"
: "${POSTGRES_RESTORE_DATABASE:?POSTGRES_RESTORE_DATABASE is required}"
: "${XISNOVE_TEST_POSTGRES_URL:?XISNOVE_TEST_POSTGRES_URL is required}"
: "${XISNOVE_TEST_POSTGRES_RESTORE_URL:?XISNOVE_TEST_POSTGRES_RESTORE_URL is required}"
: "${RUNNER_TEMP:?RUNNER_TEMP is required}"

postgres_image="postgres:18.3-alpine3.23"
archive="$RUNNER_TEMP/xisnove-postgres-smoke.dump"
umask 077

go run ./cmd/xisnove-server db migrate \
  --database-profile postgres \
  --database-url "$XISNOVE_TEST_POSTGRES_URL"

docker run --rm --network host \
  -e PGPASSWORD="$POSTGRES_PASSWORD" \
  "$postgres_image" \
  psql --host "$POSTGRES_HOST" --port "$POSTGRES_PORT" \
  --username "$POSTGRES_USER" --dbname "$POSTGRES_DATABASE" \
  --set ON_ERROR_STOP=1 \
  --command "INSERT INTO admins (id,email,password_hash,created_at) VALUES ('00000000-0000-4000-8000-000000000202','restore-smoke@example.com','hash',CURRENT_TIMESTAMP) ON CONFLICT DO NOTHING"

docker run --rm --network host \
  -e PGPASSWORD="$POSTGRES_PASSWORD" \
  "$postgres_image" \
  pg_dump --host "$POSTGRES_HOST" --port "$POSTGRES_PORT" \
  --username "$POSTGRES_USER" --format custom "$POSTGRES_DATABASE" \
  > "$archive"

docker run --rm --network host \
  -e PGPASSWORD="$POSTGRES_PASSWORD" \
  "$postgres_image" \
  dropdb --host "$POSTGRES_HOST" --port "$POSTGRES_PORT" \
  --username "$POSTGRES_USER" --if-exists "$POSTGRES_RESTORE_DATABASE"
docker run --rm --network host \
  -e PGPASSWORD="$POSTGRES_PASSWORD" \
  "$postgres_image" \
  createdb --host "$POSTGRES_HOST" --port "$POSTGRES_PORT" \
  --username "$POSTGRES_USER" "$POSTGRES_RESTORE_DATABASE"
docker run --rm --interactive --network host \
  -e PGPASSWORD="$POSTGRES_PASSWORD" \
  "$postgres_image" \
  pg_restore --host "$POSTGRES_HOST" --port "$POSTGRES_PORT" \
  --username "$POSTGRES_USER" --exit-on-error --single-transaction \
  --no-owner --dbname "$POSTGRES_RESTORE_DATABASE" \
  < "$archive"

XISNOVE_TEST_POSTGRES_URL="$XISNOVE_TEST_POSTGRES_RESTORE_URL" \
  go test ./internal/adapters/postgres -run TestMigrateAndReady -count=1

restored_count="$(
  docker run --rm --network host \
    -e PGPASSWORD="$POSTGRES_PASSWORD" \
    "$postgres_image" \
    psql --host "$POSTGRES_HOST" --port "$POSTGRES_PORT" \
    --username "$POSTGRES_USER" --dbname "$POSTGRES_RESTORE_DATABASE" \
    --tuples-only --no-align \
    --command "SELECT COUNT(*) FROM admins WHERE email='restore-smoke@example.com'"
)"
test "$restored_count" = "1"
