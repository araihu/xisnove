# Raw and systemd deployment

Raw Linux support targets glibc 2.35 or newer. Install the five release binaries
under `/usr/bin`, raw helpers under `/usr/libexec/xisnove`, systemd units under
`/etc/systemd/system`, and the two `xisnove.conf` files from `sysusers.d` and
`tmpfiles.d` under their matching `/usr/lib` directories. Then create identities and paths:

```sh
systemd-sysusers /usr/lib/sysusers.d/xisnove.conf
systemd-tmpfiles --create /usr/lib/tmpfiles.d/xisnove.conf
```

Copy `deploy/raw/server.env.example` to `/etc/xisnove/server.env`. The shipped
example selects embedded local Turso, one active server, loopback listeners, and
`/var/lib/xisnove/turso.db`. SQLite uses the same singleton wrapper. PostgreSQL
and managed Turso are replica-safe; managed Turso consumes the token only from a
private file. Never store a credentialed PostgreSQL URL in the unit environment
or command line; use the packaged server's database URL file input.

## Bootstrap and recovery

Create `/etc/xisnove/secrets` as `0700`. Supply an existing administrator password
file, or let the helper generate one, then run:

```sh
XISNOVE_ADMIN_PASSWORD_FILE=/private/input/admin-password \
XISNOVE_DATABASE_PROFILE=turso-local \
XISNOVE_DATABASE_URL=/var/lib/xisnove/turso.db \
XISNOVE_SECRET_DIR=/etc/xisnove/secrets \
XISNOVE_CONTROL_PLANE_SECRET_OWNER=xisnove:xisnove \
XISNOVE_BOOTSTRAP_STATE_DIR=/var/lib/xisnove/bootstrap \
XISNOVE_AGENT_CREDENTIAL_FILE=/var/lib/xisnove-agent/credential.json \
XISNOVE_AGENT_CREDENTIAL_OWNER=xisnove-agent:xisnove-agent \
  /usr/libexec/xisnove/bootstrap.sh
```

Offline bootstrap runs a migration bounded to 60 seconds and create-once
administrator bootstrap. Start `xisnove-server.service`, then rerun with
`XISNOVE_BOOTSTRAP_ONLINE=true` to wait for readiness, create a stable location,
enroll the Agent, and materialize `/var/lib/xisnove-agent/credential.json`.
Every process boundary records a private marker. API mutations use stable
idempotency keys; response bodies are written atomically before the next marker.
Retries neither rotate the administrator nor replace an existing Agent bundle.

Bootstrap run as root fails closed unless both ownership inputs are explicit.
`/etc/xisnove/secrets` becomes `0700 xisnove:xisnove`; its files become `0600`
and remain readable only by the control-plane identity. Agent credential is
materialized separately as `0600 xisnove-agent:xisnove-agent`. Cursor key,
notification keyring, UI cookie secret, administrator password, Agent credential,
and optional managed-Turso token are file references. No helper sends secret
values as command-line arguments or logs them. Vault Agent, OpenBao, ESO, or CSI
may materialize the same file contract with equivalent ownership.

## systemd lifecycle

`xisnove-migrate.service` is a bounded oneshot required by the server. Service
units use numeric-created service accounts, read-only system paths, explicit
writable paths, `NoNewPrivileges`, namespace restrictions, bounded shutdown,
and `Restart=on-failure`. Enable after bootstrap:

```sh
systemctl daemon-reload
systemctl enable --now xisnove-server xisnove-ui xisnove-agent
```

`run-singleton.sh` holds an atomic owner lock for the full SQLite/local-Turso
server lifetime. A live second process exits `75` without opening or changing
the database. A crash leaves an owner record; the next start proves the recorded
PID is gone before recovering it. Never share the local database directory over
an unsafe network filesystem.

For a remote Agent, install only `xisnove-agent`, copy
`deploy/raw/remote-agent.env.example` to `/etc/xisnove/agent.env`, and place its
enrolled credential at `/var/lib/xisnove-agent/credential.json` as the
`xisnove-agent` user with mode `0600`. It needs outbound HTTP(S) only.
