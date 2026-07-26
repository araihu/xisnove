# Xisnove CLI

`xisnove` is the human-oriented client for one Xisnove control plane. It is a
separate Go module and imports the control plane only through the generated
`github.com/araihu/xisnove/sdk` package.

Run it independently from the repository workspace:

```sh
GOWORK=off go run ./cmd/xisnove --help
```

## Profiles and authentication

A named profile contains a server URL and a credential reference, never a
bearer token:

```sh
GOWORK=off go run ./cmd/xisnove profile set local \
  --url http://localhost:8080

GOWORK=off go run ./cmd/xisnove profile set automation \
  --url https://xisnove.example.com \
  --credential-mode env \
  --credential-ref XISNOVE_TOKEN

GOWORK=off go run ./cmd/xisnove profile list
```

The current profile is used by default; `--profile NAME` selects another for
one invocation. Administrator login accepts a password only through stdin or a
named environment variable and stores the returned session without printing
it:

```sh
printf '%s\n' "$XISNOVE_ADMIN_PASSWORD" | \
  GOWORK=off go run ./cmd/xisnove auth login \
    --email admin@example.com --password-stdin

GOWORK=off go run ./cmd/xisnove auth logout
```

Credential modes are:

- `keyring`, the default, using the operating-system keyring service
  `xisnove-cli` and the configured account name;
- `env`, an explicit read-only automation mode;
- `file`, an explicit automation mode using an absolute token path.

Config and token files must be regular, non-symlink files with no group or
other permissions. CLI-created files use mode `0600`, parent directories use
mode `0700`, and writes are atomic. Login, logout, and `auth token create
--store-profile` therefore require a writable `keyring` or `file` credential;
the `env` mode is intentionally never mutated. Agent enrollment tokens require
an explicit absolute `--store-file` and are likewise never printed.

HTTPS is mandatory except for `localhost` and IP loopback URLs. URLs cannot
embed credentials, queries, or fragments. The default config is the platform
user-config path under `xisnove/config.yaml`; `XISNOVE_CONFIG` or `--config`
selects another file.

## SDK workflows

The generated SDK backs these command families:

- `auth login|logout|token ...`
- `monitor list|get|create|update|disable|health|incident`
- `location list|get|create|update|disable`
- `agent list|get|update|revoke|rotate-credential|revoke-generation|enrollment-token`
- `incident list|get|events`
- `discovery list|get|promote`
- `notification channel|route|delivery ...`
- `maintenance list|get|create|end|delete`
- `status` (public and credential-free)

Mutation request bodies are strict JSON or YAML decoded directly into the
generated SDK request types. Supply them with `--file PATH`, or use `--file -`
for stdin. Unknown fields, multiple documents, and inputs over 1 MiB are
rejected before any request is sent.

## Output contract

Successful data is written only to stdout. Diagnostics and errors are written
only to stderr. `--output table` is the deterministic human default;
`--output json` and `--output yaml` emit stable machine-readable documents.

Remote errors are represented as typed RFC 9457 problem details. Stable exit
classes are `1` general/server, `2` usage or validation, `4` authentication or
authorization, `5` not found, `6` conflict, and `7` rate limiting.

Every retryable mutation sends one `Idempotency-Key`. An explicit
`--idempotency-key` is silent. When omitted, the CLI generates an RFC 4122 UUID
once and writes `generated idempotency key: VALUE` to stderr so an operator can
reuse it for a manual retry.

## Development

```sh
GOWORK=off go test ./...
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
```

The test suite includes golden output tests, an import-boundary audit, SDK
fakes, and a human journey that installs and runs the exact frozen mock artifact
documented in [INTEGRATION.md](INTEGRATION.md).
