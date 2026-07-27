# First observation runbook

Assume the server is at `http://127.0.0.1:8080` and has been migrated and
bootstrapped as described in the development guide.

Run the control plane outside the infrastructure it is expected to monitor:
an external VPS or a separate Kubernetes cluster is preferable for a hybrid
homelab. Place outbound Agents inside each otherwise unreachable failure
domain, such as a home Kubernetes cluster, physical-node network, Tailscale
network, or private VPS segment. The control plane may use any supported
[database profile](database-profiles.md); Agents never access that database.

Log in and keep the opaque session token out of shell history where practical:

```bash
export XISNOVE_URL=http://127.0.0.1:8080
export ADMIN_TOKEN="$(
  curl -fsS -H 'Content-Type: application/json' \
    -d '{"email":"admin@example.com","password":"replace-with-at-least-16-characters"}' \
    "$XISNOVE_URL/v1/sessions" | jq -r .token
)"
```

Create a Location and HTTP Monitor. Probe configuration is a discriminated
`probe` object; binary `body`, `send`, and `expect` values use base64 in JSON:

```bash
export LOCATION_ID="$(
  curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" \
    -H 'Content-Type: application/json' -d '{"name":"public"}' \
    "$XISNOVE_URL/v1/locations" | jq -r .id
)"
export MONITOR_ID="$(
  curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"homepage\",\"locationId\":\"$LOCATION_ID\",\"requiredLocation\":true,\"intervalSeconds\":60,\"timeoutMillis\":5000,\"failureThreshold\":3,\"recoveryThreshold\":2,\"probe\":{\"kind\":\"http\",\"method\":\"GET\",\"url\":\"https://example.com/\",\"headers\":{},\"body\":\"\",\"expectedStatus\":[{\"minimum\":200,\"maximum\":299}],\"bodyContains\":[],\"bodyDoesNotContain\":[],\"followRedirects\":true,\"tlsMinimumRemainingSeconds\":86400}}" \
    "$XISNOVE_URL/v1/monitors" | jq -r .id
)"
```

Create a one-time enrollment token and let the Agent atomically materialize its
caller-generated credential. The enrollment command journals its credential
and idempotency key before the request, so a lost response can be retried
without creating another Agent:

```bash
umask 077
curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d "{\"locationId\":\"$LOCATION_ID\",\"expiresInSeconds\":600}" \
  "$XISNOVE_URL/v1/agent-enrollment-tokens" |
  jq -r .token > ./agent-enrollment-token
go run ./agent/cmd/xisnove-agent enroll \
  --url "$XISNOVE_URL" \
  --token-file ./agent-enrollment-token \
  --credential-file ./agent-credential.json \
  --name edge-1 \
  --capabilities http,tcp,dns
```

The resulting JSON credential bundle is mode 0600. Store it in a secret
manager, never in arguments or source control. Private targets remain denied
unless explicitly allow-listed:

```bash
XISNOVE_URL="$XISNOVE_URL" \
XISNOVE_AGENT_CREDENTIAL_FILE=./agent-credential.json \
XISNOVE_AGENT_ALLOWED_PRIVATE_CIDRS='10.0.0.0/8,192.168.0.0/16' \
XISNOVE_AGENT_CAPABILITIES='http,tcp,dns' \
go run ./agent/cmd/xisnove-agent
```

TCP probes use `{"kind":"tcp","host":"db.internal","port":5432,
"send":"","expect":""}` and may set `tlsMinimumRemainingSeconds`. DNS probes
use `{"kind":"dns","resolver":"10.0.0.53:53","name":"service.internal",
"recordType":"A","expectedValues":["10.0.0.10"]}`. Custom resolvers and
private targets must fall inside `XISNOVE_AGENT_ALLOWED_PRIVATE_CIDRS`;
resolver addresses are validated and pinned before the DNS library runs.

The scheduler emits only the latest due check after downtime and records the
number of skipped intervals. After an accepted result, a durable stale
deadline is stored as two monitor intervals plus its timeout and the lease
duration. Missing that deadline changes the required Location and aggregate
to `unknown` and opens a warning Incident exactly once.

Query the projection and active Incident:

```bash
curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$XISNOVE_URL/v1/monitors/$MONITOR_ID/health" | jq
curl -i -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$XISNOVE_URL/v1/monitors/$MONITOR_ID/active-incident"
```

Go clients should construct `sdk.ClientWithResponses` from the server URL and
attach the same bearer token through an `sdk.RequestEditorFn`; the integration
test under `integration/` is an executable example of the complete SDK flow.
