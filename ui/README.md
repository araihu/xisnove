# Xisnove UI BFF

`ui/` is a separate Go module for Xisnove's server-rendered Goshtoso BFF. It
accesses the control plane only through `internal/controlplane.Client` backed
by the public generated Go SDK. No root database,
sqlc, domain, application, or server-internal package is imported.

The SDK dependency is pinned to frozen Xisnove commit `07467ccf39e67c5cd7a68878db8c2023318e6189`.
Goshtoso is pinned to immutable guidance commit
`5d2e74e4c693ffb17a7443b8b77ed195f815cd05`; see the documented v0.0.12
exception in [`../docs/ui/goshtoso-snags.md`](../docs/ui/goshtoso-snags.md).

## Development

Generate a 32-byte cookie HMAC secret, keep the fake values server-side, and
disable Secure cookies only for local plain HTTP:

```bash
export XISNOVE_UI_COOKIE_SECRET="$(openssl rand -base64 32)"
export XISNOVE_UI_COOKIE_SECURE=false
export XISNOVE_UI_DEV_FAKE=true
export XISNOVE_UI_DEV_ADMIN_EMAIL='admin@example.test'
export XISNOVE_UI_DEV_ADMIN_PASSWORD='<local value>'
export XISNOVE_UI_DEV_SESSION='<local opaque value>'
go run ./cmd/server
```

Production defaults to Secure cookies. Put TLS at the BFF or its trusted
reverse proxy, do not enable the development fake, and configure:

```bash
export XISNOVE_UI_API_BASE_URL='https://xisnove-control-plane.example.test'
export XISNOVE_UI_REQUEST_TIMEOUT='5s'
```

The timeout bounds both the BFF request context and SDK HTTP transport.

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
