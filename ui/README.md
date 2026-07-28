# Xisnove UI BFF

`ui/` is a separate Go module for Xisnove's server-rendered Goshtoso BFF. It
accesses the control plane only through `internal/controlplane.Client` backed
by the public generated Go SDK. No root database,
sqlc, domain, application, or server-internal package is imported.

The SDK dependency is pinned to frozen Xisnove commit `07467ccf39e67c5cd7a68878db8c2023318e6189`.
Goshtoso is pinned exactly to the released `v0.0.13`. The UI uses the default
CDN-first `head.Dependencies()` contract with version-matched embedded fallback
served by `assets.Handler()`; it deliberately does not use local-only runtime
mode because Xisnove is a networked BFF. See
[`../docs/ui/goshtoso-snags.md`](../docs/ui/goshtoso-snags.md).

The default theme and X-9 v11 identity are pinned from `araihu/assets` commit
`a8a9647a6e803586c556859eb20f95ef9fcb20a1`. The theme is served same-origin
after Goshtoso's stylesheet. Header and public-status branding inline the
approved transparent v11 logo, allowing Goshtoso's usual `.dark` class to
provide the canonical surface, ink, and signal values. The favicon uses the
approved background icon at immutable `/ui/x9-v11-icon-9aef3646.svg`.

The public display name is exactly **X-9**. Goshtoso and Minimal remain
available through the theme selector; their base styles are not modified.

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

The browser smoke includes a dedicated failure harness that proves all five
versioned CDN dependencies fall back in order, including Mask, nonce/SRI/event
behavior and a terminal primary+fallback failure. The integrated smoke holds
the real monitor search request to verify pending copy and deduplication.

Set `XISNOVE_UI_BROWSER_BASE_URL` to run the browser smoke against an already
running UI, `XISNOVE_UI_BROWSER_BIN` to select Chromium, and
`XISNOVE_UI_BROWSER_SCREENSHOT_DIR` to retain login/status screenshots. The
mock-server wiring that will configure that running UI is tracked in
[`INTEGRATION.md`](INTEGRATION.md).
