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

The Chromedp smoke records 390 px and 1440 px screenshots for Goshtoso and
Minimal themes in light and dark modes. It verifies direct navigation, HTMX
refresh/navigation, browser history, no unexpected console errors, the
AppShell skip link, persistent labels/names, no page-level horizontal overflow,
and absence of opaque bearer credentials in DOM content. The smoke uses the
real generated SDK adapter against a controlled HTTP API server and asserts
the generated session, monitor page, monitor health, and public-status routes.
Handler/render tests provide the remaining state matrix without JavaScript.

The in-app browser backend was unavailable during this run (browser discovery
returned no available backend). The official standalone Chromium harness still
completed the full 24-image matrix; this record does not claim an axe scan or
manual screen-reader inspection.

Application CSS uses Xisnove-owned selectors and Goshtoso semantic custom
properties. It does not assume arbitrary utilities exist in embedded CSS;
table overflow is owned by its local results container at 390 px.
