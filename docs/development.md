# Development

Xisnove requires Go 1.26.1. Generated OpenAPI and sqlc code is committed and
must remain reproducible.

```bash
make generate
make test
make check
```

Start a local control plane:

```bash
go run ./cmd/xisnove-server db migrate --database ./dev.db
printf '%s\n' 'replace-with-at-least-16-characters' > ./dev-admin-password
chmod 600 ./dev-admin-password
go run ./cmd/xisnove-server admin bootstrap \
  --database ./dev.db \
  --email admin@example.com \
  --password-file ./dev-admin-password
go run ./cmd/xisnove-server serve --database ./dev.db
```

`serve` never applies migrations. This makes rollout order explicit and causes
readiness/startup to fail when the schema is behind. The Agent module is
verified outside the workspace:

```bash
cd agent
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
```
