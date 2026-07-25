# CLI integration handoff

## Revision boundary

- CLI branch: `codex/cli-sdk-client`
- CLI base: `4fbe157`
- Frozen API/mock task: `019f9b9d-47d6-7b82-a556-6690a8ab9383`
- Frozen contract/mock SHA: **pending coordinator handoff**

No endpoint path, operation method, generated request/response model, or mock
route is duplicated in this module. Until the frozen SHA arrives, the `auth`,
`monitor`, `location`, `agent`, `incident`, `notification`, `discovery`, and
`status` families return a typed `contract_unavailable` problem. Each family is
injectable through `command.Family`, and its runtime resolves a named profile
and bearer credential without exposing the token in output.

## Exact SDK/mock dependencies

The frozen handoff must provide:

1. A generated `github.com/araihu/xisnove/sdk` client surface for every human
   workflow in the eight remote command families, including bounded list or
   pagination operations where a family presents collections.
2. Generated request/response models and the generated RFC 9457 default problem
   response. The CLI will adapt those types; it will not define parallel API
   models.
3. An explicit `Idempotency-Key` parameter on every retryable human mutation.
   The CLI policy already resolves one stable key per invocation and exposes a
   request-editor seam.
4. A reusable mock-server lifecycle or handler from the contract track plus
   request inspection sufficient to assert bearer authentication,
   `Idempotency-Key`, content negotiation, typed problems, and pagination.
5. A frozen contract/mock commit SHA that can be merged into this task branch
   without taking coordinator-owned integration files.

The mock-server journey will be added only against that published artifact.

## Coordinator-owned repository integration

This task does not edit these global files. After the CLI commit is integrated,
the coordinator should:

- add `./cli` to `go.work`;
- add CLI `GOWORK=off` test, race, vet, and import-audit targets to `Makefile`
  and CI;
- include the CLI binary in release/package workflows and user documentation;
- pin the CLI module to the integrated SDK revision, removing the local
  development `replace` if the release process resolves the repository module
  another way.
