# UI visual acceptance record

The first integrated BFF slice covers `/login`, `/monitors`, and `/status`.
Direct requests and HTMX fragments share view components. Expected upstream
failures return a rendered 200 fragment with the upstream class in
`X-Xisnove-Response-Status`, so HTMX swaps the recovery surface; full-page
requests retain the real 502 or 504 status.

## Required states

- login success, invalid credentials, and timeout;
- monitors loading skeleton, no monitors, filtered-empty, upstream error,
  partial/unknown health, and populated success;
- public status empty, unknown/degraded/up states, active-incident warning,
  timeout, and upstream error.

## Browser matrix

The Chromedp smoke records the full 24-image happy matrix at 390 px and 1440 px
for Goshtoso and Minimal themes in light and dark modes. It also records 26
state images at both widths: invalid and timed-out login; monitor loading,
empty, filtered-empty, upstream error and partial/unknown; and public empty,
unknown, up, degraded with active incident, upstream error and timeout. These
are browser-visible captures driven through the BFF and controlled API
responses, not source-only assertions.

The smoke verifies direct navigation, HTMX navigation, Back restoration of
content/title/focus/scroll, no unexpected console errors, the AppShell skip
link, visible focus, mobile navigation open/Escape/focus return, persistent
labels and names, one desktop main scroll surface, hidden idle skeleton
geometry, Goshtoso-owned table overflow at 390 px, row-action focusability and
absence of opaque bearer credentials in DOM content. It uses the generated SDK
adapter and asserts session, monitor page, monitor health and public-status
routes.

The automated P1 accessibility scan is an in-repo browser equivalent rather
than axe. It fails on missing accessible names, broken labels, duplicate IDs,
invalid main/banner landmarks, nested headers and measurable text contrast. It
passed Minimal light and the other required theme/mode combinations. Desktop
placeholder width is measured using the rendered font and padding. No manual
screen-reader session is claimed.

Application CSS uses Xisnove-owned selectors and Goshtoso semantic custom
properties. It does not assume arbitrary utilities exist in embedded CSS;
table overflow is owned by its local results container at 390 px.
