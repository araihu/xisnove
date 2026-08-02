# Seasonal assets Phase A

Phase A stages the released Arai Hu Assets runtime and X-9 baseline without
enrolling any Xisnove page. It is intentionally path-disjoint from the active
Milestone 4A, global-search, UI-server, template, generated, and static-site
work.

## Frozen source

- Xisnove base: `d2c0d3c7a731bc8cb2ede30663a7c8d1112400fb`
- Assets release: `v0.1.1`
- Assets commit: `74c36ed038ad127cab72d10ac6c5a8ca79646244`
- Phase A implementation: `21b58265a5f9ca3dd7d7d84f3dbd176d32587a14`
- Runtime SHA-256: `a936193b4fed8120e6cb3423f19d3e2ddb0ba32266dc4e5f02a98f5261853709`
- Runtime SRI: `sha384-oPH7l1vK9vKP1Dn+18sO3yEXlz4ts6KzPEQl0SW4Y/+im05gOaamNNaQAf6bGH/n`
- Public channel: `https://araihu.com/assets/releases/current`

The embedded runtime is byte-identical to both
`runtime/campaign/v1.js` and `dist/campaign/v1.js` at the release tag. The
embedded logo, marks, and favicon are byte-identical to the catalog-selected
X-9 assets. Their declared SHA-256 values are enforced by focused Go tests.
Arai Hû organization projects are expressly authorized by the upstream NOTICE
to use and redistribute these assets.

## Staged package

`ui/internal/seasonalassets` exposes metadata, defensive runtime bytes, and an
unmounted HTTP handler for these future same-origin routes:

- `/assets/campaign/v1.js`
- `/ui/seasonal/v0.1.1/x9-logo.svg`
- `/ui/seasonal/v0.1.1/x9-mark.svg`
- `/ui/seasonal/v0.1.1/x9-mark-reverse.svg`
- `/ui/seasonal/v0.1.1/x9-favicon.svg`

The runtime route uses a five-minute cache because its public path is not
versioned. Versioned fallback assets use a one-year immutable cache. GET and
HEAD are supported; mutation methods are rejected.

## Integration boundary

Phase A does not:

- mount the handler;
- change templates or generated templ output;
- change CSP;
- add DOM enrollment hooks;
- alter `xisnove-theme` preference semantics;
- touch the untracked static site;
- enable a campaign.

Phase B must begin from a new commit that freezes the active primary work and
tracks `site/`. Its integrator owns the small server/template/CSP wiring diff,
templ regeneration, and browser evidence. The runtime must remain external and
deferred; no `unsafe-inline` or wildcard origin is allowed. A stored explicit
`xisnove-theme` must set `data-theme-source="preference"` before runtime
execution.

## Deferred-assurance receipt

- Exact implementation SHA: `21b58265a5f9ca3dd7d7d84f3dbd176d32587a14`
- Skipped gates: BFF mounting, rendered enrollment contract, CSP integration,
  static-site adoption, browser campaign/failure/opt-out matrix, and full UI
  suite.
- Current green evidence: exact release SHA-256/SRI checks,
  `GOWORK=off go test ./internal/seasonalassets -count=1`, and staged-diff
  validation.
- Risk: the frozen upstream runtime has no bounded fetch/image deadline;
  restoration associates hooks by query order; click listeners bind only hooks
  present at startup. Review also noted raw URL restoration, direct storage
  mutations, redundant SVG attribute handling, and an ignored extra argument.
  Changing any item locally would invalidate the released SHA-256 and SRI.
- Affected future paths: `ui/internal/view/pages.templ`, generated templ output,
  `ui/internal/web/server.go`, server/view/browser tests, CSP, and tracked
  `site/` equivalents.
- Acceptance criteria: Assets owners triage the seven runtime findings and
  publish a new coordinated release/SRI for accepted fixes, or the integration
  owner records explicit risk acceptance; Xisnove then proves bounded failure,
  preference precedence, opt-out, HTMX hook behavior, CSP, and baseline
  restoration in realistic browser tests.
- Trigger: primary Milestone 4A/global-search/site state becomes a committed,
  pushed integration base and upstream runtime disposition is recorded.
- Owners: Arai Hu Assets owner for runtime findings; Xisnove control plane for
  integration and browser acceptance.

Until both trigger conditions hold, Phase A may be merged only as dormant,
unmounted preparation. It must not be wired into a deployable Xisnove surface.
