# Docker Compose deployment

The Compose bundle keeps the control plane outside monitored infrastructure. Its
default profile runs one SQLite server, the SDK-only UI/BFF, and one colocated
outbound Agent. Ports bind loopback by default. Copy the example directory to a
private host directory; do not edit committed examples with credentials.

## First installation

Requirements are the Docker Compose plugin or `docker-compose`, `openssl`, `curl`,
and `jq`. Detection prefers `docker compose`; `COMPOSE_COMMAND=docker-compose`
selects the standalone binary explicitly. Set an administrator
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

## Local UI development

Use the dev overlay for the UI and the disposable HTTP/TCP/DNS monitor
fixtures. It builds the local `ui/` module, enables `AUTH_MODES=none` with the
development fake control plane, and rebuilds the containers when their sources
change:

```sh
docker compose \
  -f deploy/compose/compose.yaml \
  -f deploy/compose/compose.dev.yaml \
  watch ui monitor-http monitor-tcp monitor-dns
```

The fixtures share the `xisnove-service` entrypoint. Their Compose commands
select the service type (`http`, `tcp`, or `dns`) and expose host ports
`18080`, `19090`, and `15353` respectively. From another Compose service, use
`http://monitor-http:8080/healthz`, `monitor-tcp:9090` with the `PONG` response,
or DNS resolver `monitor-dns:5353` for `service.test A 192.0.2.10`. The host
ports can be changed with `XISNOVE_FIXTURE_HTTP_PORT`,
`XISNOVE_FIXTURE_TCP_PORT`, and `XISNOVE_FIXTURE_DNS_PORT`.

The development fake control plane seeds matching non-public monitors named
`Compose HTTP`, `Compose TCP`, and `Compose DNS`, including their HTTP, TCP,
and DNS probe definitions. It records an initial state tick and advances an
in-memory historical state/availability stream every five seconds by default;
override that cadence with `XISNOVE_UI_DEV_TICK_INTERVAL`. Unknown health emits
a state tick but no availability sample, preserving the gap semantics. This is
synthetic development data, not durable control-plane persistence; actual probe
scheduling and durable history require the normal server + Agent stack.

To run the fixture CLI directly, build `build/package/Dockerfile.service` and
pass the type as the entrypoint's first argument, for example:

```sh
docker build -f build/package/Dockerfile.service -t xisnove-service:watch .
docker run --rm -p 18080:8080 xisnove-service:watch \
  http --listen=0.0.0.0:8080
```

The overlay keeps the production Compose file on its prebuilt-image path. Stop
the watcher with `Ctrl-C`; remove the dev container with:

```sh
docker compose \
  -f deploy/compose/compose.yaml \
  -f deploy/compose/compose.dev.yaml \
  down
```

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
server services mount no local database storage. Bootstrap starts
`XISNOVE_SERVER_REPLICAS` instances (default `2`) and publishes each API on a
collision-free loopback port. Discover a deterministic instance with:

```sh
docker compose -f deploy/compose/compose.yaml port --index 1 server-remote 8080
```

Use the fixed UI endpoint at `http://127.0.0.1:8081`; internal UI and Agent
traffic uses Compose service discovery. Local Turso remains a
raw/singleton profile and is not advertised as Compose multi-replica.

Deploy a separate outbound Agent near private targets with
`deploy/compose/remote-agent.yaml`. Copy the one-time enrolled credential bundle
to an owner-only file on that host; the Agent initiates all control-plane traffic.

## Operations

Check SQLite readiness at `http://127.0.0.1:8080/readyz`. For remote profiles,
append `/readyz` to the instance endpoint returned by `compose port`. UI
readiness remains `http://127.0.0.1:8081/readyz`. SQLite data lives in
`xisnove-data`; restart does
not remove it. `docker compose down` preserves volumes. Destructive cleanup is
explicit:

```sh
docker compose -f deploy/compose/compose.yaml down --volumes --remove-orphans
```

Back up SQLite before upgrades. Stop the old server, run the bounded expand
migration once, then start the replacement. A second SQLite or local-Turso
server must fail with retryable exit `75`; investigate instead of deleting a
live lock.
