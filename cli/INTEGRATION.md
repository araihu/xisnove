# CLI integration handoff

## Revision boundary

- Original CLI branch: `codex/cli-sdk-client`
- Original CLI base: `4fbe157`
- Frozen API/mock task: `019f9b9d-47d6-7b82-a556-6690a8ab9383`
- Integrated API/SDK checkpoint: `9741fed1ef08`
- Root module version: `v0.0.0-20260726121002-9741fed1ef08`
- Mock command package: `github.com/araihu/xisnove/cmd/xisnove-mock@v0.0.0-20260726121002-9741fed1ef08`

The integrated CLI does not duplicate the API/mock implementation. Its separate `go.mod` pins
the published root-module pseudo-version and all CLI code imports Xisnove only
through `github.com/araihu/xisnove/sdk`. There are no duplicated endpoint paths,
request/response models, database packages, or control-plane internals.

## SDK and mock coupling

The command implementations call only generated `ClientWithResponses`
operations and generated request/response types. Authentication and mutation
headers use the SDK-provided `sdk.WithBearerToken` and
`sdk.WithIdempotencyKey` request editors.

`internal/journey/mock_test.go` installs the exact mock package above into a
temporary `GOBIN`, starts it on an ephemeral loopback port, and exercises:

- profile creation with a private file credential;
- administrator login and logout without secret output;
- authenticated monitor pagination and an explicitly idempotent mutation;
- incident, discovery, and notification-channel reads;
- public status without credential lookup;
- a typed RFC 9457 rate-limit scenario and stable exit code.

At the frozen head, the handwritten mock dispatcher implements sessions, API
tokens, monitors, incidents/events, discovery, notification collection lists,
and public status. Its generated interface contains the broader location,
agent, maintenance, and notification mutation surface, but the handwritten
dispatcher returns `404` for those routes. CLI coverage for those generated SDK
families therefore uses typed HTTP fakes and command-topology tests rather than
inventing mock behavior in this module.

The API task also reported that the API-owned generated Agent client is
authoritative; do not run the legacy Agent-module generator when integrating
this branch.

## Repository integration

The control-plane integration:

- integrated the frozen API/mock head before the CLI so the repository-local
  SDK matches the pinned standalone dependency;
- adds `./cli` to `go.work` and verifies both workspace-enabled and
  `GOWORK=off` builds;
- runs vet, race tests, the import-boundary audit, typed HTTP fakes, and the
  frozen mock journey through Make and CI;
- retains the published pseudo-version for standalone module verification,
  while workspace builds resolve the repository-local root module.

Release/package workflows still need to include the CLI binary in Milestone 6.
