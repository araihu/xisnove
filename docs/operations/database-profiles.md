# Database profiles

Xisnove exposes one application and repository contract across four relational
profiles. Select it consistently for `db migrate`, `admin bootstrap`, and
`serve` with `--database-profile` and `--database-url`. Managed Turso also
requires `--database-auth-token-file`.

| Profile | Pinned driver | CGO | Active Xisnove servers | Migration family | Backup and restore | Credential input | CI coverage | v1 state |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `sqlite` | `modernc.org/sqlite` v1.54.0 | No | One | SQLite-compatible Goose files | Online application backup; restore to a new file | Local path in `--database-url` | Every change, including full storage journey | Stable |
| `turso-local` | `turso.tech/database/tursogo` v0.7.1 | No; `purego` calls the bundled Rust C ABI | One | SQLite-compatible Goose files | Quiesced whole-volume snapshot | Local path in `--database-url` | Every change, including full storage journey | Evolving |
| `turso-cloud` | `github.com/tursodatabase/libsql-client-go` at `9d5d30a29a60` | No | Multiple stateless servers | Atomic remote batches from the SQLite-compatible files | Provider point-in-time recovery or export | `libsql://` URL plus token file | Protected weekly, release, and manual repository plus full-journey tests | Evolving |
| `postgres` | `github.com/jackc/pgx/v5` v5.10.0 | No | Multiple stateless servers | Native PostgreSQL Goose files | PostgreSQL 18 `pg_dump` and `pg_restore` | PostgreSQL URL supplied through protected configuration | Every change with a PostgreSQL 18 service and full storage journey | Stable |

SQLite and local Turso deliberately reject a requested server replica count
above one. Their single process may use a bounded `database/sql` handle, but a
shared file or volume is not a distributed lease coordinator. PostgreSQL and
managed Turso coordinate scheduler, lease, staleness, and Incident claims in
the database and support multiple stateless Xisnove servers after migrations
complete.

## Two different Turso profiles

`turso-local` embeds the newer Rust Turso Database engine through `tursogo`.
It is a local database profile and Xisnove v1 does not enable its remote-sync
mode. Even when sync is added later, pushing and pulling file state would not
by itself provide the linearizable claim coordination required by active
control-plane replicas.

`turso-cloud` is a remote managed libSQL profile. It uses a database-scoped JWT
and the remote service as the coordination boundary. Its transport cannot use
Goose's pinned-connection migration lifecycle, so Xisnove applies each shared
migration and version record as one atomic remote batch.

## Selection and startup

Migrate before serving. Readiness requires the exact current schema version,
not merely a successful connection:

```bash
xisnove-server db migrate \
  --database-profile postgres \
  --database-url "$XISNOVE_DATABASE_URL"

xisnove-server serve \
  --database-profile postgres \
  --database-url "$XISNOVE_DATABASE_URL" \
  --replicas 2
```

Keep URLs, passwords, and database tokens in secret-backed environment or
files. Xisnove accepts a separate managed-Turso token file so Kubernetes
Secrets, External Secrets Operator, Vault, or OpenBao can materialize it
without coupling the storage port to a specific secret manager.

See [backup and restore](backup-restore.md) for profile-specific recovery and
[persistence conformance](persistence-conformance.md) for the executable
cross-profile contract.
