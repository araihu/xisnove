# UI integration handoff

## Frozen API and SDK dependency

The UI module consumes `github.com/araihu/xisnove/sdk` from immutable root
module commit `07467ccf39e67c5cd7a68878db8c2023318e6189`. Its `go.mod` records the
corresponding pseudo-version, and release checks run with `GOWORK=off` so a
local checkout cannot silently replace that dependency.

`ui/internal/controlplane.SDKClient` constructs the generated response client
with the production HTTP transport. Authentication, revocation, aggregate
public status, cursor monitor pages, and health enrichment all use generated
operations and types. The UI adds presentation models only; it has no copied
wire model or handwritten endpoint client.

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
