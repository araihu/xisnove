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
8. **Theme transitions obscured acceptance-state truth.** Applying the next
   theme marker and scanning immediately can sample Goshtoso's
   `transition-colors` between its light and dark values. The acceptance driver
   now updates Alpine state and DOM markers together, asserts the exact settled
   markers and applies an application-owned test root that disables transitions
   for deterministic screenshots. The contrast scanner also needed a canvas
   conversion because Chrome reports some computed colors as `oklch()`.
9. **Several rendered semantic pairs needed consumer-level contrast fixes.**
   The complete scanner exposed a 4.41:1 primary action and lower dark action,
   warning/success/status combinations in the required themes. Xisnove uses
   product semantic action tokens and isolated root classes for primary
   actions, alerts and health badges. Monitor row actions use a native link so
   Goshtoso's link-specific dark utility cannot override the application color
   contract. Labels and status text remain present; no Goshtoso source or
   generated file was patched.
10. **Navbar intentionally hides focus outlines without a per-link class
    hook.** A source dive found `focus:outline-hidden` on both public nav links;
    `NavLink` exposes attributes but not a dedicated root-class field. Xisnove
    scopes a visible outline and box shadow through the public navbar's
    `NavClass`. The keyboard suite caught both missing indicators before the
    override. A public link-class option would avoid this consumer CSS escape
    hatch.
11. **Screenshot quality is an encoding selector, not only a compression
    setting.** Chromedp's `FullScreenshot` follows the DevTools API: values
    below 100 produce JPEG bytes. The first acceptance run used quality 90 but
    wrote `.png` filenames. Xisnove now requests 100, validates the PNG magic
    bytes before every write and scans the whole evidence directory after the
    run. This was harness misuse, not a Goshtoso defect.
