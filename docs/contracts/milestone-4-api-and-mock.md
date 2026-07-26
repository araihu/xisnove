# Milestone 4 API contract and mock server

Milestone 4 freezes the application contract that the UI, CLI, operator, and
agents consume. `api/openapi.yaml` is the canonical OpenAPI 3.1.2 document and
uses the JSON Schema 2020-12 dialect.

The contract adds:

- revocation of the current administrator session;
- create, list, get, update, and revoke operations for scoped API tokens;
- list and get operations for Locations, plus list, get, replace, and disable
  operations for Monitors;
- Agent listing, reads, revocation, and one-time credential rotation;
- cursor-paged Incident and IncidentEvent reads;
- bounded Agent discovery batches, an administrator catalog, and explicit
  promotion;
- the single unauthenticated aggregate status page at `GET /v1/status-page`,
  including recent daily uptime history.

Every operation declares `security` and `x-xisnove-scopes` explicitly.
Protected operations fail closed. The only unauthenticated operations are
session creation, Agent enrollment, and public status. List resources accept
opaque `cursor` values and return `page.nextCursor`. Retryable mutations accept
`Idempotency-Key`. Errors use `application/problem+json` with
RFC 9457 members plus the stable `code` and `correlationId` extensions.

## Generated surfaces

Run all generators from the repository root:

```sh
make generate
```

Generated artifacts are:

- `sdk/generated.gen.go`: the complete public client and response models;
- `internal/mockapi/generated.gen.go`: the complete strict server contract;
- `internal/adapters/httpapi/generated.gen.go`: the complete production strict
  interface, transport models, and embedded contract;
- `agent/internal/controlplane/generated.gen.go`: enrollment, heartbeat, work,
  results, and discovery-batch Agent client subset.

`go test ./api -run TestGeneratedAPIBindingsAreCurrent` regenerates every
artifact in temporary files and compares it byte-for-byte with the committed
output.

`agent/oapi-codegen.yaml` is the sole Agent generation filter. It contains only
enrollment, heartbeat, lease, result upload, and `upsertDiscoveryCandidates`.
Both ordinary `make generate` and isolated
`cd agent && GOWORK=off go generate ./...` produce the same committed client.

The production and mock generators both cover the complete canonical contract.
The production `Server` fills that strict interface operation by operation as
the application slices land; the deterministic mock routes every advertised
operation through generated strict request decoding. The mock also validates
the complete HTTP request against the embedded OpenAPI document, buffers the
handwritten deterministic response, and validates its status, media type, and
body schema before releasing it to the caller. A nonconforming fixture is
replaced by an RFC 9457 `mock_response_failed` response.

Formal `Idempotency-Key` parameters appear in generated mutation method
signatures. Callers may pass a generated params value or pass `nil` and add
authentication and retry identity through request editors:

```go
editors := []sdk.RequestEditorFn{
    sdk.WithBearerToken(token),
    sdk.WithIdempotencyKey("monitor-update-router-1"),
}
```

## Run the mock

```sh
go run ./cmd/xisnove-mock
```

The default base URL is `http://127.0.0.1:8089`. Override it with:

```sh
go run ./cmd/xisnove-mock -listen 0.0.0.0:9090
```

The mock is deterministic, in-memory, has no database, makes no outbound
requests, and contains no real secrets. Restarting it restores the fixtures.
Idempotency entries are isolated by principal, operation, key, and canonical
request-body hash. Reusing a key with a changed body returns
`idempotency_key_reused`. Credential-issuing operations retain only a replay
marker and return `credential_already_issued` instead of replaying plaintext.

### Fixture credentials

| Purpose | Value |
|---|---|
| Administrator email | `admin@xisnove.test` |
| Administrator password | `mock-password` |
| Administrator session returned by login | `xisnove_mock_session_admin_0000000000000001` |
| Full API token | `xisnove_mock_api_full_0000000000000000000001` |
| Read-only API token | `xisnove_mock_api_read_0000000000000000000001` |
| Agent credential | `xisnove_mock_agent_000000000000000000000001` |

Log in:

```sh
curl --fail-with-body \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@xisnove.test","password":"mock-password"}' \
  http://127.0.0.1:8089/v1/sessions
```

Read public status without a credential:

```sh
curl --fail-with-body http://127.0.0.1:8089/v1/status-page
```

The initial fixtures include two Monitors, one open critical Incident with one
event, one pending discovery candidate, one notification channel and route, and
one delivered notification. Notification write fixtures accept configuration
but never return the submitted service URL or bearer token.

### Stable failure scenarios

Set `X-Xisnove-Mock-Scenario` on any request:

| Header value | HTTP status | Problem code |
|---|---:|---|
| `validation` | 422 | `mock_validation` |
| `unauthorized` | 401 | `unauthorized` |
| `forbidden` | 403 | `insufficient_scope` |
| `not-found` | 404 | `not_found` |
| `conflict` | 409 | `mock_conflict` |
| `rate-limit` | 429 | `mock_rate_limit` |
| `server-error` | 503 | `mock_unavailable` |

For example:

```sh
curl -i \
  -H 'X-Xisnove-Mock-Scenario: rate-limit' \
  http://127.0.0.1:8089/v1/status-page
```

The rate-limit fixture includes `Retry-After: 60`. Every scenario returns an
RFC 9457 body with a deterministic correlation ID unless the caller supplies
`X-Request-ID`.

## Handoff notes

Control plane:

- implement session revocation, API-token persistence/scope middleware,
  management queries and mutations, Incident/Event reads, discovery storage
  and promotion, and public aggregation behind the complete strict interface;
- replace deprecated offset pagination on older notification and maintenance
  handlers with opaque cursors, then remove the compatibility offsets;
- implement each remaining generated strict operation on the real `Server`;
  the interface and router are intentionally already complete.

UI:

- keep the administrator credential in the BFF only;
- use `GET /v1/status-page` directly only for the public read model;
- use `page.nextCursor` opaquely and exercise loading/error states with the stable
  scenario header in local development.

CLI:

- use `sdk.WithBearerToken` and `sdk.WithIdempotencyKey`;
- persist only the one-time plaintext returned by API-token creation;
- treat cursor values as opaque and expose RFC 9457 `code`, `correlationId`,
  and field errors without printing credentials.
