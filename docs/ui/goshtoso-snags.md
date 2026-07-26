# Goshtoso v0.0.12 consumer notes and snags

The foundation started on `github.com/araihu/goshtoso v0.0.12`. The integrated
UI read the globally installed consumer skill and its complete application
patterns, visual acceptance, and generated component reference from immutable
guidance commit `5d2e74e4c693ffb17a7443b8b77ed195f815cd05`.

## Component inventory

The foundation uses the v0.0.12 public APIs for `head.Dependencies`,
`alert.Alert`, `button.Button`, `card.Card`, `navbar.Navbar`, and
`textinput.TextInput` (username only). It uses package-owned dimensions and
button functional options; no removed render helper, compatibility alias, or
manually edited generated templ file is present. Goshtoso's precompiled CSS is
served through `assets.Handler()` directly at `/assets/`, so this slice has no
consumer CSS artifact to regenerate.

The integrated slice additionally uses `appshell.AppShell`,
`pageheader.PageHeader`, `toolbar.Toolbar`, `table.Table`,
`emptystate.EmptyState`, and `skeleton.Skeleton`. These packages are absent
from v0.0.12, so the module is pinned to the exact pseudo-version
`v0.0.13-0.20260726064127-5d2e74e4c693` rather than a moving branch or
`latest`. This is an explicit exception to the earlier v0.0.12 pin, required
by the newer immutable handoff. No Goshtoso source, generated component, or
removed internal helper was copied or patched in Xisnove. A future Goshtoso
release should make this pin a normal tagged dependency.

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

4. **Application-pattern components are newer than the latest pinned tag.**
   The required packages compile only from guidance commit `5d2e74e`. Resolving
   its pseudo-version also encountered a transient `sum.golang.org` HTTP 500;
   a module-scoped direct Git fetch completed the pin. Ordinary `GOWORK=off go
   mod tidy`, generation, and tests then work without a local Goshtoso checkout.
5. **Sidebar Overlay omits Escape and trigger-focus restoration.** The public
   overlay supplies a labelled native trigger, backdrop, panel and Alpine open
   state, but it does not close on Escape or return focus. Xisnove composes a
   window Escape handler around the public component. Those contracts belong
   in the component because every application drawer needs them.
6. **Navbar renders action components twice.** Desktop and mobile action slots
   render the same component value into the DOM simultaneously. Stateful or
   labelled controls therefore duplicate IDs even when one copy is visually
   hidden. Xisnove uses an application top bar inside AppShell and keeps Navbar
   for public links without actions.
7. **Navbar cannot be the AppShell Header value without nested banner
   landmarks.** Both components render a `header`. The application-pattern
   guidance says AppShell Header is content rather than another header, so
   Xisnove uses a native top-bar container there. A public Navbar content-only
   mode would remove this composition trap.
