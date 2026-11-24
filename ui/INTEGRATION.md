# UI integration handoff

## Frozen API and SDK dependency

The UI module consumes `github.com/araihu/xisnove/sdk` from immutable root
module commit `07467ccf39e67c5cd7a68878db8c2023318e6189`. Its `go.mod` records the
corresponding pseudo-version, and release checks run with `GOWORK=off` so a
local checkout cannot silently replace that dependency. The standalone UI targets this
control-plane API. Root release v0.0.2 omits the public status page, cursor-based
monitor listing, and session revocation operations that the UI uses. Upgrade
the SDK explicitly when the target server implements those operations.

`ui/internal/controlplane.SDKClient` constructs the generated response client
with the production HTTP transport. Authentication, revocation, aggregate
public status, cursor monitor pages, and health enrichment all use generated
operations and types. The UI adds presentation models only; it has no copied
wire model or handwritten endpoint client.

## Remaining control-plane integration work

- add `./ui` to the development workspace once the root SDK supports the UI contract;
- expose the frozen mock browser smoke through the root Makefile and CI;
- pass the production API base URL, cookie HMAC secret, TLS/cookie policy, and
  timeout settings through release/Compose/Helm packaging; and
- ensure release checks build the UI with `GOWORK=off`, so it cannot resolve
  unpublished local SDK code.

The root `make check` target and the Dagger root job both run `make -C ui check`,
which regenerates the UI templates, checks for drift, tests the UI, and runs
`go vet` with the workspace disabled.
