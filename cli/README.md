# Xisnove CLI

`xisnove` is the human-oriented client for one Xisnove control plane. It is a
separate Go module and, once the API contract is frozen, imports Xisnove only
through `github.com/araihu/xisnove/sdk`.

The contract-independent profile commands are available now:

```sh
GOWORK=off go run ./cmd/xisnove profile set local \
  --url http://localhost:8080

GOWORK=off go run ./cmd/xisnove profile set automation \
  --url https://xisnove.example.com \
  --credential-mode env \
  --credential-ref XISNOVE_TOKEN

GOWORK=off go run ./cmd/xisnove profile list
```

## Credentials

Profiles store a URL and a credential reference, never a bearer token.

- `keyring` is the default. Tokens are read from the operating-system keyring
  under service `xisnove-cli` and the configured account name.
- `env` reads the named environment variable and is deliberately read-only.
- `file` reads or atomically writes an absolute token path. Token and config
  files must be regular, non-symlink files with no group/other permissions;
  CLI-created files use mode `0600` and directories use mode `0700`.

HTTPS is mandatory except for `localhost` and IP loopback URLs. URLs cannot
embed credentials, queries, or fragments.

The default config is the platform user-config path under
`xisnove/config.yaml`. `XISNOVE_CONFIG` or `--config` selects another file.
`--profile NAME` overrides the current profile for one invocation.

## Output contract

Successful data is written only to stdout. Diagnostics and errors are written
only to stderr. `--output table` is the deterministic human default;
`--output json` and `--output yaml` emit stable machine-readable documents.

Remote errors are represented as typed RFC 9457 problem details. Stable exit
classes are: `1` general/server, `2` usage or validation, `4` authentication or
authorization, `5` not found, `6` conflict, and `7` rate limiting.

Every retryable mutation sends exactly one `Idempotency-Key`. An explicit
`--idempotency-key` is silent. When omitted, the CLI generates an RFC 4122 UUID
once and writes `generated idempotency key: VALUE` to stderr so an operator can
reuse it for a manual retry.

## Development

Run the module independently from the repository workspace:

```sh
GOWORK=off go test ./...
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
```

SDK command families and the mock-server journey remain intentionally gated on
the frozen API/mock revision documented in [INTEGRATION.md](INTEGRATION.md).
