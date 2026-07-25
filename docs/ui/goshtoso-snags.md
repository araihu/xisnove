# Goshtoso v0.0.12 consumer notes and snags

Before editing the UI module, this task read the complete v0.0.12
`CHANGELOG.md`, `docs/MIGRATING_COMPONENT_API.md`,
`docs/COMPONENT_MODEL.md`, consumer integration guide, and repository
instructions from the released module at commit
`5d95d62e531ab9a4c2a7ce33ff6c4d3942181e0d`.

## Component inventory

The foundation uses the v0.0.12 public APIs for `head.Dependencies`,
`alert.Alert`, `button.Button`, `card.Card`, `navbar.Navbar`, and
`textinput.TextInput` (username only). It uses package-owned dimensions and
button functional options; no removed render helper, compatibility alias, or
manually edited generated templ file is present. Goshtoso's precompiled CSS is
served through `assets.Handler()` directly at `/assets/`, so this slice has no
consumer CSS artifact to regenerate.

## Snags

1. **Password input has an unsafe no-JavaScript fallback.** The published
   `textinput.TypePassword` template emits `x-bind:type` but no static
   `type="password"`. Before Alpine initializes, and when JavaScript is
   unavailable, HTML defaults that input to plaintext. The handler/component
   test caught this. Xisnove uses a native `type="password"` input with
   Goshtoso theme classes until the component has a safe static fallback.
2. **Exact application-facing fields required a source lookup.** The component
   model and consumer guide explain constructor styles, but the exact navbar,
   text input, and action fields needed reading the released `types.go` files.
   The released module archive also omits the demo site's separate Go module,
   so the guide's site examples were not locally inspectable from the pinned
   dependency.
3. **Fresh templ tool bootstrapping was order-sensitive.** `go mod tidy` could
   not resolve the local `internal/view` package before its first generated Go
   file existed. The reproducible bootstrap is `go get -tool
   github.com/a-h/templ/cmd/templ@v0.3.1020`, then `go tool templ generate`,
   then `go mod tidy`. The module Makefile now exposes the normal steady-state
   `make generate` command.

The first snag is worth fixing upstream because it affects every Goshtoso
consumer that expects password semantics to remain safe without Alpine.
