# UI integration handoff

## Frozen API and mock dependency

The API/mock track is active in Codex task
`019f9b9d-47d6-7b82-a556-6690a8ab9383`. This branch was built from
`codex/milestone-3-notifications` at `4fbe157` and deliberately contains no
control-plane URL, endpoint path, generated SDK import, or copied API model.

The next UI slice is blocked on a coordinator handoff containing all of:

1. the published frozen API/mock commit SHA;
2. the generated SDK module/import path and its client construction contract;
3. the mock server's supported startup command and base-URL output;
4. the generated authentication/session and public-status operations and
   RFC 9457 response types; and
5. the command that proves the mock matches the frozen OpenAPI document.

After that handoff, implement an adapter under `ui/internal/controlplane/`
that satisfies the existing narrow interface with generated SDK calls. Extend
the interface only for workflows backed by frozen generated types. Then add an
API-base-URL setting to `ui/cmd/server`, point the browser harness's running UI
at the mock, and cover successful/error/timeout status rendering. Do not add a
hand-written HTTP client or parallel request/response structs.

## Requested global integration changes

These files are outside this task's write scope and must be applied by the
control-plane coordinator after merging the UI branch:

- add `./ui` to the root development `go.work` use list;
- add root Makefile targets that run `GOWORK=off make -C ui check` and the
  browser/mock smoke;
- add CI jobs for the standalone UI module, templ generation drift, and the
  frozen mock browser smoke;
- pass the production API base URL, cookie HMAC secret, TLS/cookie policy, and
  timeout settings through release/Compose/Helm packaging; and
- ensure release checks build the UI with `GOWORK=off`, so it cannot resolve
  unpublished local SDK code.

The root OpenAPI, SDK, server, Makefile, `go.work`, CI, and packaging files were
not modified by this branch.
