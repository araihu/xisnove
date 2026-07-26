# UI visual acceptance record

The first integrated BFF slice covers `/login`, `/monitors`, the selected
monitor Detail Workspace at `/monitors?selected=<id>`, and `/status`.
Direct requests and HTMX fragments share view components. Expected upstream
failures return a rendered 200 fragment with the upstream class in
`X-Xisnove-Response-Status`, so HTMX swaps the recovery surface; full-page
requests retain the real 502 or 504 status.

## Required states

- login success, invalid credentials, and timeout;
- monitors loading skeleton, no monitors, filtered-empty, upstream error,
  partial/unknown health, and populated success;
- selected-monitor detail success plus the explicit partial state for probe
  history that is not exposed by the current public API;
- public status empty, unknown/degraded/up states, active-incident warning,
  timeout, and upstream error.

## Browser matrix

The Chromedp smoke records the full 32-image happy matrix at 390 px and 1440 px
for Goshtoso and Minimal themes in light and dark modes. It also records 26
state images at both widths: invalid and timed-out login; monitor loading,
empty, filtered-empty, upstream error and partial/unknown; and public empty,
unknown, up, degraded with active incident, upstream error and timeout. These
are browser-visible captures driven through the BFF and controlled API
responses, not source-only assertions. The final evidence is exactly 58
PNG-encoded files
under `/Users/guilhermecastro/.codex/visualizations/2026/07/24/019f9527-817a-7953-932e-e262e7351b8a/xisnove-ui-goshtoso-guidance-6eef695`.
The browser harness requests lossless encoding, rejects bytes without the
eight-byte PNG signature before writing, and re-reads every `.png` artifact at
the end of the run. The final evidence was also checked with the system `file`
utility rather than trusting filename extensions.

The smoke verifies direct navigation, HTMX navigation, Back restoration of
content/title/focus/scroll, no unexpected console errors, the AppShell skip
link, visible focus, mobile navigation open/Escape/focus return, persistent
labels and names, one desktop main scroll surface, hidden idle skeleton
geometry, Goshtoso-owned table overflow at 390 px, row-action focusability and
absence of opaque bearer credentials in DOM content. It uses the generated SDK
adapter and asserts session, monitor page, monitor health and public-status
routes. Loading also asserts that the result surface has zero visible geometry
while the skeleton is active and that the skeleton reuses the result surface's
horizontal boundaries. The selected-monitor route is opened directly, keeps
one `h1`, preserves the Operations List, places the metadata rail to the right
at 1440 px and after the main detail content at 390 px, and remains free of page
overflow.

The automated P1 accessibility scan is an in-repo browser equivalent rather
than axe. It runs after every one of the 58 captures and fails on missing
accessible names, broken labels, duplicate IDs, invalid main/banner landmarks,
nested headers and measurable text contrast. Transparent backgrounds are
alpha-composited through every rendered ancestor; a one-pixel canvas converts
modern CSS colors such as `oklch()` into sRGB before WCAG luminance is measured.
It passed Minimal light and all other required theme/mode/state combinations.
Desktop placeholder width is measured using the rendered font and padding.

Sequential Tab traversal was exercised for login, desktop monitors, selected
monitor detail, mobile monitors with its open drawer, and public status. The
test compares focus to visible DOM order, rejects hidden focus stops, requires
a computed outline or box shadow at every stop, activates representative
native links, and retains the dedicated skip-link, row-action, drawer
Escape/focus-return, HTMX and Back checks. No manual screen-reader session is
claimed.

Manual original-resolution review covered representative 390 px and 1440 px
login, populated monitors, selected-monitor detail, loading, partial health,
status and active-incident captures across both themes and modes. It found no
clipping, stale loading content, lost hierarchy or unreadable semantic state in
those representatives; this is visual inspection, not an assistive-technology
session.

Public status results intentionally do not inherit the 16 rem minimum height
used by monitor result/loading surfaces. Their grid aligns content at the start,
and browser geometry checks require empty, unknown and success alerts to remain
compact and top-aligned at both acceptance widths.

Application CSS uses Xisnove-owned selectors and Goshtoso semantic custom
properties. It does not assume arbitrary utilities exist in embedded CSS;
table overflow is owned by its local results container at 390 px.
