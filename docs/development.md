# Development

Xisnove requires Go 1.26.1. Generated OpenAPI and sqlc code is committed and
must remain reproducible.

Public-core changes must preserve the dependency and context rules in the
[Open Core extension guide](architecture/open-core.md). The external-module
fixture is deliberately tested outside the workspace:

```bash
cd integration/testdata/external-module
GOWORK=off go test ./...
```

Application services depend on `application/port.UnitOfWork`; persistence
adapters must propagate the supplied context into both `View` and `Transact`
callbacks. Add or extend suites in `contracttest` when introducing an adapter
behavior that all supported relational profiles must share.

```bash
make generate
make test
make check
```

Start a local control plane:

```bash
go run ./cmd/xisnove-server db migrate \
  --database-profile sqlite --database-url ./dev.db
printf '%s\n' 'replace-with-at-least-16-characters' > ./dev-admin-password
chmod 600 ./dev-admin-password
go run ./cmd/xisnove-server admin bootstrap \
  --database-profile sqlite \
  --database-url ./dev.db \
  --email admin@example.com \
  --password-file ./dev-admin-password
go run ./cmd/xisnove-server serve \
  --database-profile sqlite --database-url ./dev.db
```

The same three commands accept `turso-local`, `turso-cloud`, and `postgres`.
Managed Turso additionally requires `--database-auth-token-file`; the token is
trimmed after reading and is never accepted in the database URL. The old
`--database ./dev.db` form remains a deprecated SQLite-only alias during v1.

`serve --replicas N` declares the expected number of server replicas. Values
greater than one require the replica-safe `postgres` or `turso-cloud` profile;
SQLite and local Turso deliberately reject them.

`serve` never applies migrations. This makes rollout order explicit and causes
readiness/startup to fail when the schema is behind. The Agent module is
verified outside the workspace:

```bash
cd agent
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
```

The workspace path is also supported:

```bash
go test -race ./agent/...
```

Protocol tests use local HTTP/TLS, TCP, and UDP/TCP DNS servers. The Agent
never resolves a custom DNS resolver behind the DNS client's back: policy
validation produces the IP endpoint passed to the library. Run the focused
control-plane integration repeatedly when changing scheduling or result
projection:

```bash
go test -race ./integration -run TestProtocolBreadth -count=10
```
