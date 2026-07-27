# Docker Compose deployment

The Compose bundle keeps the control plane outside monitored infrastructure. Its
default profile runs one SQLite server, the SDK-only UI/BFF, and one colocated
outbound Agent. Ports bind loopback by default. Copy the example directory to a
private host directory; do not edit committed examples with credentials.

## First installation

Requirements are Docker Compose, `openssl`, `curl`, and `jq`. Set an administrator
password through a private file or the process environment, then run the bounded
bootstrap helper:

```sh
export XISNOVE_ADMIN_PASSWORD_FILE=/private/input/admin-password
deploy/compose/bootstrap.sh
```

The helper uses `umask 077`, creates `deploy/compose/secrets/` and its files with
mode `0600`, runs expand migration and administrator bootstrap, waits for
`/readyz`, enrolls the colocated Agent, atomically materializes its credential,
then starts all services. Administrator and Agent identities are stable across
retries. Completed boundaries are recorded in `.bootstrap-state`; rerunning the
helper resumes without rotating byte-identical secret files. Delete the input
password only after confirming login.

Direct `docker compose up` assumes bootstrap already completed. Example files
contain only `CHANGE-ME` placeholders. Never commit `secrets/`, `.env`, or the
bootstrap state directory.

## Profiles

SQLite is the default and supports exactly one server. The named PostgreSQL
profile adds a disposable, trust-authenticated database for local evaluation:

```sh
set -a
. deploy/compose/postgres.env.example
set +a
COMPOSE_PROFILES=postgres deploy/compose/bootstrap.sh
```

For external PostgreSQL, replace the example URL through a private URL file once
before first bootstrap, set `XISNOVE_DATABASE_URL_SOURCE_FILE` to that input,
and use `external-postgres.env.example`; never put a credentialed URL in a
committed env file or rendered config. Managed Turso is a real remote profile,
never an emulation: provide its URL by private environment/config, its token via
`XISNOVE_TURSO_AUTH_TOKEN_FILE`, and set `COMPOSE_PROFILES=managed-turso`.
Multiple server replicas are allowed only for PostgreSQL and managed Turso. Their
server services mount no local database storage. Local Turso remains a
raw/singleton profile and is not advertised as Compose multi-replica.

Deploy a separate outbound Agent near private targets with
`deploy/compose/remote-agent.yaml`. Copy the one-time enrolled credential bundle
to an owner-only file on that host; the Agent initiates all control-plane traffic.

## Operations

Check readiness at `http://127.0.0.1:8080/readyz` and UI readiness at
`http://127.0.0.1:8081/readyz`. SQLite data lives in `xisnove-data`; restart does
not remove it. `docker compose down` preserves volumes. Destructive cleanup is
explicit:

```sh
docker compose -f deploy/compose/compose.yaml down --volumes --remove-orphans
```

Back up SQLite before upgrades. Stop the old server, run the bounded expand
migration once, then start the replacement. A second SQLite or local-Turso
server must fail with retryable exit `75`; investigate instead of deleting a
live lock.
