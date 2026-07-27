# UI state and action invariant ledger

The current monitor surface is read-only. Its observable effect count is the
number of control-plane reads or session lifecycle calls; no monitor mutation is
offered or accepted by the BFF.

| Route or state | Action or request | Allowed? | Expected response | Context preserved | Focus or destination | Effect count |
|---|---|---:|---|---|---|---:|
| login / signed out | submit valid credentials | yes | 303 PRG to `/monitors`; secure session cookie | theme and mode | authoritative monitor document | one session creation |
| login / invalid credentials | submit | no session | rendered sign-in error with fresh CSRF | no password echo; theme/mode | sign-in surface | zero sessions |
| monitor shell / signed in | logout with valid CSRF | yes | 303 PRG to `/login`; cookie expired | no cached task page | login | one revocation |
| monitor shell / missing or forged CSRF | logout | no | policy error | session remains | error surface | zero revocations |
| authenticated shell / ready | open global search by trigger or `Ctrl+K`/`⌘K` | yes, read-only | persistent Goshtoso search dialog; no operational index is downloaded | current route/query/cursor/selection/theme/mode | search input inside trapped dialog | zero control-plane reads before a qualifying query |
| global search / open | type a qualifying query | yes, read-only | debounced BFF request backed by the public search API; bounded, server-ranked results | current route/query/cursor/selection/theme/mode | search input; active result resets only for the latest query owner | one active query; superseded requests aborted and stale responses ignored |
| global search / open | choose a destination | yes, read-only | native or HTMX navigation to the canonical result URL | theme/mode and destination identity | destination heading or selected-resource drawer | navigation and authoritative resource reads only |
| global search / open | close with Escape | yes | dialog closes without navigation | complete route/query/cursor/selection/theme/mode | exact search trigger | zero control-plane effects |
| global search / request failure | retry, edit query, or close | retryable | visible scoped recovery; failure is distinct from an empty result set | complete route/query/cursor/selection/theme/mode | search input, retry control, or exact trigger after close | one failed bounded query per attempt |
| monitor inventory / ready | search | yes, read-only | HTMX inventory fragment and canonical `q` URL | query, theme, mode | same search control and caret | one monitor read window; duplicate submit deduplicated |
| monitor inventory / ready | select monitor | yes, read-only | selected collection plus detail; canonical `selected` URL | query/cursor/theme/mode | selected detail heading | monitor and bounded health reads only |
| monitor detail / Back or Forward | restore selection | yes, read-only | fresh `no-store` SDK read replaces history snapshot | URL/query/selected/theme/mode | same selected detail heading | one fresh monitor read window |
| monitor detail / overlapping history or pageshow refreshes | apply newest owner only | yes, read-only | older request is aborted and ignored even if its transport resolves later | captured URL and newest generation | newest state or recovery only | at most one owning response may mutate the DOM |
| monitor detail / history revalidation failure | retry authoritative refresh | yes, read-only | stale detail/actions are replaced by a visible in-shell alert and retry control | authoritative URL and theme/mode; no stale monitor state | alert heading, then refreshed detail heading | one failed read per attempt; one fresh read on retry |
| monitor inventory / upstream failure | retry | retryable | swappable in-shell fragment; upstream status in response header | query/cursor/selected | explicit response target | reads only; no mutation |
| authenticated unknown route | return to monitors | yes | shell-preserving recovery link | authenticated shell/theme/mode | monitor inventory | zero before navigation |
| any UI route | forged monitor POST/PUT/DELETE | no route | 405/404 problem response | server truth unchanged | in-shell recovery when authenticated | zero monitor mutations |

Browser acceptance holds the real search request, attempts a duplicate click,
and requires one control-plane list read, pending copy, disabled submitter, final
query URL, and restored focus/caret. Login and logout remain native PRG flows;
all routed pages use `Cache-Control: no-store` so history recovery revalidates
authoritative monitor state. Acceptance mutates the fixture revision and health
before Back, then requires both markers in the refreshed DOM. Separate 503 and
rejected-fetch paths must remove the stale detail, focus the recovery heading,
and restore current state only after an explicit retry. The ordering fixture
deliberately resolves two ignored-abort transports out of order and proves an
older success can replace neither a newer state nor a newer recovery surface.

Global search takes its interaction model from Manja's OpenAPI documentation
search: one persistent Goshtoso `SearchField`/`SearchModal` pair, one
`GlobalShortcut` owner, stable IDs, compact metadata-rich results, safe
canonical URLs, focus trapping, and ArrowUp/ArrowDown/Enter/Escape behavior.
X-9 does not reuse Manja's download-once `ItemsURL` model because operational
inventory is mutable and access-controlled. The BFF sends a debounced, bounded
query to a public Xisnove search operation; the control plane owns matching,
ranking, and authorization. The client owns opening, keyboard navigation,
cancellation, and latest-query response ownership. Static navigation
destinations may be merged locally without being presented as server results.
Browser acceptance covers trigger and shortcut parity, focus return, minimum
query behavior, loading, empty, error and retry states, abort/sequence
protection, safe result URLs, and the absence of duplicate shortcut owners
while navigation preserves current URL state.

## Planned live status updates

- [ ] Add Server-Sent Events (SSE) for monitor health, incident transitions,
  Agent presence, and discovery-catalog invalidation. Define the control-plane
  stream in the public OpenAPI contract and relay it through an authenticated
  BFF endpoint so browser code never receives an API bearer token.
- [ ] Treat each event as an invalidation hint, not authoritative state. The
  BFF must re-read the affected bounded resource through the generated SDK and
  render the same HTMX fragment used by direct navigation before changing the
  DOM.
- [ ] Specify stable event IDs, `Last-Event-ID` resume, heartbeat comments,
  bounded per-client buffers, slow-consumer disconnect, reconnect backoff, and
  graceful shutdown. Event payloads must contain identifiers and transition
  metadata only, never credentials, probe diagnostics, or notification URLs.
- [ ] Preserve current URL, selection, focus, theme, mode, and newest-owner
  ordering while applying a live fragment. Fall back to bounded polling and
  authoritative page revalidation when SSE is unavailable or the resume window
  has expired.
- [ ] Add contract, BFF, browser, reconnect, duplicate-event, out-of-order,
  authorization, backpressure, and multi-replica tests before enabling SSE by
  default.
