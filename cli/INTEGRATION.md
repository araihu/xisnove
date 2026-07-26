# CLI integration handoff

## Revision boundary

- CLI branch: `codex/cli-sdk-client`
- CLI base: `4fbe157`
- Frozen API/mock task: `019f9b9d-47d6-7b82-a556-6690a8ab9383`
- Frozen API/mock feature commit: `80099d6`
- Authoritative pushed API/mock head: `def0e9efefe0714547c81cbb9ae8609fdbb65be1`
- Root module version: `v0.0.0-20260725235705-def0e9efefe0`
- Mock command package: `github.com/araihu/xisnove/cmd/xisnove-mock@v0.0.0-20260725235705-def0e9efefe0`

The CLI branch does not merge the API/mock branch. Its separate `go.mod` pins
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

## Coordinator-owned repository integration

This task does not edit global integration files. The coordinator should:

- integrate the frozen API/mock head before or with these CLI commits so the
  repository-local SDK matches the pinned dependency;
- add `./cli` to `go.work`;
- add CLI `GOWORK=off` test, race, vet, and import-audit targets to `Makefile`
  and CI;
- include the CLI binary in release/package workflows and user documentation;
- decide whether release builds retain the published pseudo-version or resolve
  the integrated repository module through `go.work`.
