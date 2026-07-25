# Xisnove UI BFF

`ui/` is a separate Go module for Xisnove's server-rendered Goshtoso BFF. It
depends on exactly `github.com/araihu/goshtoso v0.0.12` and accesses the
control plane only through `internal/controlplane.Client`. No root database,
sqlc, domain, application, or server-internal package is imported.

The frozen generated-SDK adapter is intentionally pending. Until its commit is
handed off, the runnable server requires an explicit development fake and does
not accept an API base URL or invent any endpoint.

## Development

Generate a 32-byte cookie HMAC secret, keep the fake values server-side, and
disable Secure cookies only for local plain HTTP:

```bash
export XISNOVE_UI_COOKIE_SECRET="$(openssl rand -base64 32)"
export XISNOVE_UI_COOKIE_SECURE=false
export XISNOVE_UI_DEV_FAKE=true
export XISNOVE_UI_DEV_ADMIN_USERNAME='<local value>'
export XISNOVE_UI_DEV_ADMIN_PASSWORD='<local value>'
export XISNOVE_UI_DEV_SESSION='<local opaque value>'
go run ./cmd/server
```

Production defaults to Secure cookies. Put TLS at the BFF or its trusted
reverse proxy and do not enable the development fake.

## Commands

```bash
make generate       # official templ generation; never edit *_templ.go
make test           # module tests with the local workspace disabled by CI
make check          # generation drift, tests, and vet
make browser-smoke  # real Chromium smoke; local TLS server by default
```

Set `XISNOVE_UI_BROWSER_BASE_URL` to run the browser smoke against an already
running UI, `XISNOVE_UI_BROWSER_BIN` to select Chromium, and
`XISNOVE_UI_BROWSER_SCREENSHOT_DIR` to retain login/status screenshots. The
mock-server wiring that will configure that running UI is tracked in
[`INTEGRATION.md`](INTEGRATION.md).
