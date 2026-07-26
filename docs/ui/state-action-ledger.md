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
| monitor inventory / ready | search | yes, read-only | HTMX inventory fragment and canonical `q` URL | query, theme, mode | same search control and caret | one monitor read window; duplicate submit deduplicated |
| monitor inventory / ready | select monitor | yes, read-only | selected collection plus detail; canonical `selected` URL | query/cursor/theme/mode | selected detail heading | monitor and bounded health reads only |
| monitor detail / Back or Forward | restore selection | yes, read-only | fresh `no-store` SDK read replaces history snapshot | URL/query/selected/theme/mode | same selected detail heading | one fresh monitor read window |
| monitor inventory / upstream failure | retry | retryable | swappable in-shell fragment; upstream status in response header | query/cursor/selected | explicit response target | reads only; no mutation |
| authenticated unknown route | return to monitors | yes | shell-preserving recovery link | authenticated shell/theme/mode | monitor inventory | zero before navigation |
| any UI route | forged monitor POST/PUT/DELETE | no route | 405/404 problem response | server truth unchanged | in-shell recovery when authenticated | zero monitor mutations |

Browser acceptance holds the real search request, attempts a duplicate click,
and requires one control-plane list read, pending copy, disabled submitter, final
query URL, and restored focus/caret. Login and logout remain native PRG flows;
all routed pages use `Cache-Control: no-store` so history recovery revalidates
authoritative monitor state.
