# Backup and restore

Always restore into a new database or an empty destination. Validate the
restored schema and a known observation before changing the server's database
configuration. Keep database credentials in secret files, `PGPASSFILE`, or a
secret manager rather than command arguments or backup artifacts.

## SQLite

Xisnove exposes the modernc SQLite online-backup API through the server CLI.
The destination must not already exist; partial output is removed on failure,
and a completed artifact is owner-readable and owner-writable only.

```bash
xisnove-server db backup \
  --database-profile sqlite \
  --database-url /var/lib/xisnove/xisnove.db \
  --output /var/backups/xisnove-$(date -u +%Y%m%dT%H%M%SZ).db
```

Reads and writes may continue during the backup. Do not copy the main database
file directly while WAL mode is active.

Restore by placing the artifact at a new path, preserving mode `0600`, then
run readiness before serving it:

```bash
chmod 0600 /var/lib/xisnove-restored/xisnove.db
xisnove-server db migrate \
  --database-profile sqlite \
  --database-url /var/lib/xisnove-restored/xisnove.db
```

Start one server against the restored path and require `/readyz` plus a known
Monitor health or Incident read before changing production traffic.

## Local Turso Database

The pinned `tursogo` API does not expose an online backup primitive for an
ordinary local database. `xisnove-server db backup --database-profile
turso-local` therefore fails closed; it never copies a live database or WAL.

Use a quiesced filesystem or volume snapshot:

1. stop Xisnove and verify no process has the database open;
2. snapshot the complete volume or directory containing the Turso database and
   every associated WAL/state artifact as one unit;
3. restore that complete unit to an empty path;
4. run `db migrate` with `--database-profile turso-local`, then start one
   server and require readiness and a known observation read.

For a future synced local profile, explicitly push pending changes and invoke
the supported checkpoint API before shutdown. A checkpoint folds committed WAL
frames into the main file, but it is not itself a backup.

## Managed Turso Cloud

Managed Turso backup and restore are provider operations, so the application
CLI fails closed instead of pretending that a remote SQL copy is a durable
backup. Turso creates backups at commit and supports point-in-time recovery by
creating a new database from the original at a chosen timestamp. The restored
database receives a new URL and requires a new token.

Use the current Turso CLI or Platform API procedure documented in [Point-in-Time
Recovery](https://docs.turso.tech/features/point-in-time-recovery). After
creation:

1. mint a database-scoped token for the restored database;
2. run `xisnove-server db migrate --database-profile turso-cloud` against the
   new URL and token file;
3. require readiness and a known observation read;
4. update Xisnove's secret/configuration and roll replicas gradually;
5. retain the old database through the recovery window, then delete it
   explicitly.

For portable offline retention, Turso also provides a snapshot export command,
but exported snapshots may lag the newest commits. See [Turso database
export](https://docs.turso.tech/cli/db/export).

## PostgreSQL

Use the PostgreSQL 18 client utilities rather than application code. `pg_dump`
creates a consistent export while the database remains in use; custom archives
are restored with `pg_restore`. See the official [`pg_dump`
reference](https://www.postgresql.org/docs/current/app-pgdump.html) and
[`pg_restore` reference](https://www.postgresql.org/docs/current/app-pgrestore.html).

```bash
export PGPASSFILE=/run/secrets/xisnove-pgpass
pg_dump --format=custom --file=/var/backups/xisnove.dump "$XISNOVE_DATABASE_URL"

createdb xisnove_restore_smoke
pg_restore \
  --exit-on-error \
  --single-transaction \
  --no-owner \
  --dbname=xisnove_restore_smoke \
  /var/backups/xisnove.dump
```

Run Xisnove migration/readiness against `xisnove_restore_smoke` and query a
known Monitor, result, health projection, and Incident. Restore archives can
contain executable database definitions from the source; only restore backups
from a trusted PostgreSQL administrative boundary.
