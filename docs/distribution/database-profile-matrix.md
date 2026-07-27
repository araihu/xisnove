# Database profile matrix

| Profile | Location | Server replicas | Migration serialization | Upgrade model | Persistent path / secret |
|---|---|---:|---|---|---|
| SQLite | local file | exactly 1, singleton | transaction plus file/process ownership | downtime replacement | `/var/lib/xisnove/xisnove.db` on one RWO volume |
| local Turso | embedded local database | exactly 1, singleton | transaction plus file/process ownership | downtime replacement; raw/Compose only in v1 | `/var/lib/xisnove/turso.db` on local durable storage |
| PostgreSQL | remote service | replica-safe, 1+ | database advisory lock with bounded timeout | online expand, mixed N-1/N, then contract | URL from mounted Secret; no server data volume |
| managed Turso | remote service | replica-safe, 1+ | database-backed CAS migration lease with bounded expiry | online expand, mixed N-1/N, then contract | URL and token from mounted Secret; no server data volume |

SQLite and local Turso reject replica counts above one before rendering or
startup. A second active process must fail deterministically without changing
the database. Managed Turso is not emulated by local Turso: protected
credentialed acceptance proves its remote multi-replica behavior.

Every profile runs explicit migrations. PostgreSQL and managed Turso use a
bounded install/upgrade Job before compatible workloads. SQLite stops the old
StatefulSet pod, confirms singleton ownership, attaches its RWO volume, runs a
bounded migration init container, then starts the replacement. Local Turso
uses the equivalent single-process raw/Compose sequence.

Expand migrations preserve the N-1 readable interval. Contract migration waits
until no live incompatible process-version lease remains. Contention returns a
stable retryable exit class; migration errors fail closed. Remote profiles use
provider-native backup/restore. SQLite/local Turso require a consistent file
backup while the singleton is stopped or through the documented online backup
command.
