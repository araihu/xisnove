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

The default theme is the organization-owned Arai Hû theme, pinned from
`araihu/assets` commit `f841fe90b967b16ab2ad9efaee5aa636468e1afd` and served
same-origin as `/ui/araihu-f841fe90.css` after Goshtoso's stylesheet. The
Goshtoso and Minimal base themes remain available through the theme selector
and are not modified. The shared repository is public and licenses the theme
under Apache-2.0 as of commit `a5e1afb1f3df2cc50aa88c9558370fd8fd177e9b`.
The Xisnove favicon is likewise pinned from the canonical
`logos/xisnove-favicon.svg` at `araihu/assets` commit `bffc2acfc9380eaf84473abfeaacbba625ac73d5`
and served from the immutable, versioned `/ui/xisnove-bffc2ac.svg` URL. Its
SHA-256 is `4df17d9b60b9999bed10e1e937ac5fdce433245ff5c4bdf43bd81605a4372d61`,
matching `brand/canonical-assets-v3.sha256`.
The preceding `/ui/xisnove-81300f5.svg` bytes remain served for one rolling
release so old and new replicas never break immutable asset URLs.

## Development

For local development, select the explicit no-auth mode. It is restricted to
the fake control plane; the default remains authenticated `basic` mode. Generate
a 32-byte cookie HMAC secret and disable Secure cookies only for local plain HTTP:

```bash
export AUTH_MODES=none
export XISNOVE_UI_COOKIE_SECRET="$(openssl rand -base64 32)"
export XISNOVE_UI_COOKIE_SECURE=false
export XISNOVE_UI_DEV_FAKE=true
go run ./cmd/server
```

`AUTH_MODES` is a comma-separated list. Supported values are `basic`, `none`,
and `oidc`; `basic` is the default. `none` must be used alone and only with
`XISNOVE_UI_DEV_FAKE=true`. OIDC is reserved and currently fails startup until
its provider integration exists.

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
