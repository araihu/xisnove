# Goshtoso v0.1.14 consumer notes and historical snags

The consumer now uses exact `github.com/araihu/goshtoso v0.1.14` (tag target
`dfb1b392371048247ddda07786c6f197c08b0ca6`). Its default
`head.Dependencies()` loader is exercised under five ordered CDN failures and
the embedded fallbacks, with SRI, CSP nonce propagation, Mask, Alpine plugins,
HTMX, combobox, ready/fallback/error events, and terminal rejection asserted in
Chromium. The v0.1.14 manifest also emits the first-party Goshtoso bundle as a
same-origin dependency before Alpine and HTMX. Xisnove remains CDN-first for
third-party runtimes; `WithLocalRuntime` is not justified for this networked
application.

The v0.1.14 release does not require a Go API or runtime migration for this
consumer. The notes below retain the earlier v0.0.13 findings as history while
the module pin and verification target move forward.

The foundation started on `github.com/araihu/goshtoso v0.0.12`, followed the
immutable guidance checkpoint `5d2e74e4c693ffb17a7443b8b77ed195f815cd05`,
and is now verified against the tagged v0.1.14 API and updated global consumer
skill.

The authenticated monitor console additionally consumes the released
`github.com/araihu/goshtoso-app-shells v0.1.4` `consoleshell` module. Its
same-origin shell assets are mounted at `/consoleshell/assets/`; the shell owns
the frame, responsive navigation, and HTMX lifecycle while Xisnove owns the
monitor content, remote search, appearance controls, and session actions.
Authenticated fragments are rendered through `consoleshell.Fragment`; the
single current monitor destination does not request a navigation OOB update,
avoiding a sidebar swap when a full-page public route is active.

The public Manja documentation shell is the visual reference for this
composition: a compact top bar with a square menu trigger, one brand mark, an
outlined global search trigger with a keyboard hint, a mode toggle, and an
account menu. Xisnove keeps that composition application-owned while reusing
the public Goshtoso shell slots; the responsive drawer contains only the
status link. The authenticated shell now uses the immutable X-9 mark and
favicon from the staged seasonal asset set under the explicit
`/ui/seasonal/v0.1.1/` routes; those bytes are served by the UI's
`seasonalassets.Handler` and are not copied into the view layer.

## Component inventory

The foundation uses the public APIs for `head.Dependencies`,
`alert.Alert`, `button.Button`, `card.Card`, `navbar.Navbar`, and
`textinput.TextInput` (username only). It uses package-owned dimensions and
button functional options; no removed render helper, compatibility alias, or
manually edited generated templ file is present. Goshtoso's precompiled CSS is
served through `assets.Handler()` directly at `/assets/`, so this slice has no
consumer CSS artifact to regenerate.

The integrated slice additionally uses `appshell.AppShell`,
`pageheader.PageHeader`, `toolbar.Toolbar`, `table.Table`,
`emptystate.EmptyState`, and `skeleton.Skeleton`. They are now available from
the released v0.1.14 tag. No Goshtoso source, generated component, or removed
internal helper is copied or patched in Xisnove.

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

4. **The initial application-pattern dependency required a pre-release pin.**
   Before v0.0.13, the required packages compiled only from guidance commit
   `5d2e74e`; resolving it also encountered a transient `sum.golang.org` HTTP
   500. This is resolved by the exact v0.0.13 tag, and ordinary `GOWORK=off go
   mod tidy`, generation, and tests work without a local Goshtoso checkout.
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
12. **Detail Workspace is an application composition, not a single public
    component.** The immutable component reference and current public API were
    inspected before adding the selected-monitor workspace. Goshtoso provides
    the Page Header, badges, alerts, links, table, and shell vocabulary, but no
    dedicated detail/definition-list primitive; Xisnove therefore composes the
    workspace with application-owned semantic CSS variables. The public API
    exposes current monitor health but no probe-result history read operation,
    so the workspace renders an explicit partial "history unavailable" state
    instead of inventing a BFF endpoint or copying a wire model. This is an
    expected application boundary rather than a Goshtoso defect.

13. **The v0.0.13 default changes CSP from a static allow-list into a nonce
    contract.** The loader creates scripts dynamically and propagates its nonce;
    Xisnove therefore generates a request nonce through `templ.WithNonce`, uses
    it on its own external `app.js`, and combines `strict-dynamic`, the default
    unpkg origin, self, and Alpine's required `unsafe-eval`. The old self-only
    policy silently forced every primary onto fallback, so a working page did
    not prove the intended CDN-first path.
14. **Recovered network failures remain visible as browser network diagnostics.**
    Acceptance records JavaScript exceptions/events rather than treating an
    expected failed primary request as a console-cleanliness defect. A separate
    terminal test catches the rejected `ready` promise and requires exactly one
    `goshtoso:dependency-error` event.
15. **Sidebar Overlay v0.0.13 closes on Escape but still does not restore focus
    or expose a dynamic open/close trigger label.** Xisnove keeps a small
    application script that reflects `aria-expanded`, changes the label, and
    restores trigger focus. The public `PanelPositionClass` and
    `BackdropPositionClass` solve viewport ownership; the browser measures
    positive viewport intersection.
16. **A fixed `top-16` overlay offset is not coupled to the application's
    rendered header.** At 390 px the previous stacked controls made the header
    about 163 px tall while the drawer began at 64 px. Xisnove now keeps the
    compact header on one row and publishes its measured bottom through a
    shared CSS custom property. A `ResizeObserver` updates the value. Mobile
    navigation panel/backdrop and the modal Drawer panel start below the header;
    the modal Drawer overlay still covers the full viewport so header controls
    cannot remain pointer-interactive behind `aria-modal=true`. Browser
    acceptance verifies header hit-testing plus reachable first/last controls.
17. **Component minimum size does not cover every composed interactive
    surface.** A native row link rendered 39 by 44 px and the public navigation
    menu trigger rendered 24 by 24 px. Xisnove adds an application boundary of
    44 by 44 px for buttons and native/action links. The scanner checks this in
    every 390 px state; this is useful upstream feedback for mobile navigation
    and icon-button defaults.
18. **Dependency readiness is a behavioral gate, not only a loading promise.**
    Focus checks could race the application's handler registered on
    `window.goshtosoDependencies.ready`. The browser harness now awaits that
    promise and two animation frames before keyboard traversal, then proves
    focused actions remain focused for another two frames. This avoids both
    false focus failures and screenshots captured during runtime settlement.
19. **`WithLoadingText` needs the requesting ancestor to receive
    `htmx-request`.** A form with an external `hx-indicator` disabled its
    submitter and displayed the skeleton, but left the form without the class
    that switches Goshtoso's visible label. A `textContent` assertion initially
    hid this because both labels remained in the DOM. Xisnove now uses one
    indicator class on the form and skeleton, and acceptance requires
    `innerText` to contain `Searching…`. Goshtoso's contract is correct, but
    documenting this external-indicator composition would prevent a subtle
    consumer failure.
20. **Public mobile navigation inherited only the browser's one-pixel focus
    outline.** Exhaustive action acceptance found the icon button after the
    earlier primary-only scanner passed. Xisnove now gives every focused button
    a three-pixel semantic ring; the scanner requires at least 3:1 indicator
    contrast for each visible action, not only the first primary action.
21. **The signed-out terminal problem composition had no recovery action.** A
    zero-action acceptance state is now a failure rather than an automatic
    skip, so timeout pages provide a native `Return to sign in` link. The
    off-canvas AppShell skip link is deliberately excluded only from hover
    measurement and remains covered by its keyboard-specific contract.
22. **Resolved: the organization theme initially lived in a private,
    unlicensed asset repository.** The canonical Arai Hû stylesheet remains
    pinned at content commit `f841fe90b967b16ab2ad9efaee5aa636468e1afd`.
    `araihu/assets` became public and added Apache-2.0 plus an explicit NOTICE
    at commit `a5e1afb1f3df2cc50aa88c9558370fd8fd177e9b`; anonymous repository,
    raw-theme, and license reads now return 200. Xisnove keeps an attributed,
    same-origin pinned copy after Goshtoso's stylesheet for deterministic
    availability, not to work around access or licensing.
23. **Drawer has no initial-open option, and an early `drawer:open` event can
    be lost before Alpine initializes an HTMX-inserted instance.** Xisnove
    retries the public event for one bounded second and stops only when both the
    Drawer Alpine state and visible panel agree. Geometry alone was racy while
    `x-cloak` and `x-show` settled. The same initializer runs after direct load,
    HTMX settle, history restore and authoritative `innerHTML` recovery. Focus
    moves to the detail heading after two animation frames so the Focus plugin's
    `x-trap` cannot overwrite it. A public initial-open option or idempotent
    imperative controller would remove this consumer lifecycle glue.
24. **Drawer close requests intentionally do not own application route
    state.** Xisnove intercepts `drawer:close-request`, follows the native/HTMX
    close link, and restores focus to the matching table row after settle. This
    keeps Escape, overlay, close button, URL, selected styling and browser
    history under one server-backed identity instead of hiding a still-selected
    resource only in Alpine state.
25. **Search `ItemsURL` v0.0.13 is a lazy client index, not server-backed
    typeahead.** Xisnove must keep authorization, ranking and result bounds in
    the control plane, so it composes the public `button.Button` as the trigger
    with an application-owned remote dialog adapter and single Ctrl/Cmd+K owner with
    debounce, abort, stale-response rejection, loading, empty, error/retry and
    active-option semantics. The upstream Goshtoso task implemented a generic
    `RemoteSource` contract on branch `codex/server-backed-search` at
    `24c23c340a04e0d8892bad9cf5e2ca94dd2dd262`; Xisnove can remove this adapter
    after that work is reviewed, merged and released. Until then it remains on
    the released v0.1.14 pin and does not depend on an unreleased commit.
26. **Product identity remains application-owned and version-pinned.** The
    Goshtoso shell correctly accepts consumer content rather than prescribing a
    logo. Xisnove now renders the canonical independent V10 logo/marks from
    `araihu/assets@ab01f1a0f592e4f1398173df04e4f8fc013cb21a`, serves each SVG on an
    immutable same-origin route, and retains the two previous favicon routes
    for cached documents. This required no Goshtoso source dive or CSS escape
    hatch; responsive light/dark mark selection stays inside the application
    brand slot.
27. **`SearchField`'s global shortcut did not fire without a paired Goshtoso
    modal.** Chromium delivered a real `Ctrl+K` event with `ctrlKey=true`, but
    the field's `x-on:keydown.window` handler did not dispatch
    `goshtoso-search-open` in the split app-owned-modal composition. Xisnove now
    installs exactly one application owner for trigger clicks and Ctrl/Cmd+K.
    The upstream `RemoteSource` integration should cover this split composition
    so the application owner can be removed after release.
28. **`SearchField` v0.0.13 embeds an inline runtime that the required nonce CSP
    blocks.** The rendered field called `goshtosoSearchField(...)`, but its raw
    inline script had no request nonce. Chromium reported the function and
    `searchId` as undefined. The selected release exposes no external-runtime or
    nonce option for this split server-backed dialog. Xisnove therefore composes
    the public `button.Button` trigger with its existing same-origin application
    script. A nonce-aware component script or external first-party runtime would
    remove this consumer fallback.
