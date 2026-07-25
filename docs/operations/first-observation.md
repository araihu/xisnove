# First observation runbook

Assume the server is at `http://127.0.0.1:8080` and has been migrated and
bootstrapped as described in the development guide.

Log in and keep the opaque session token out of shell history where practical:

```bash
export XISNOVE_URL=http://127.0.0.1:8080
export ADMIN_TOKEN="$(
  curl -fsS -H 'Content-Type: application/json' \
    -d '{"email":"admin@example.com","password":"replace-with-at-least-16-characters"}' \
    "$XISNOVE_URL/v1/sessions" | jq -r .token
)"
```

Create a Location and HTTP Monitor:

```bash
export LOCATION_ID="$(
  curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" \
    -H 'Content-Type: application/json' -d '{"name":"public"}' \
    "$XISNOVE_URL/v1/locations" | jq -r .id
)"
export MONITOR_ID="$(
  curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"homepage\",\"locationId\":\"$LOCATION_ID\",\"requiredLocation\":true,\"intervalSeconds\":60,\"timeoutMillis\":5000,\"failureThreshold\":3,\"recoveryThreshold\":2,\"http\":{\"method\":\"GET\",\"url\":\"https://example.com/\",\"expectedStatus\":200,\"bodyContains\":\"\",\"followRedirects\":true}}" \
    "$XISNOVE_URL/v1/monitors" | jq -r .id
)"
```

Create a one-time enrollment token and enroll the Agent:

```bash
export ENROLLMENT_TOKEN="$(
  curl -fsS -H "Authorization: Bearer $ADMIN_TOKEN" \
    -H 'Content-Type: application/json' \
    -d "{\"locationId\":\"$LOCATION_ID\",\"expiresInSeconds\":600}" \
    "$XISNOVE_URL/v1/agent-enrollment-tokens" | jq -r .token
)"
curl -fsS -H 'Content-Type: application/json' \
  -d "{\"token\":\"$ENROLLMENT_TOKEN\",\"name\":\"edge-1\",\"capabilities\":[\"http\"]}" \
  "$XISNOVE_URL/v1/agent-enrollments" |
  jq -r .credential > ./agent-credential
chmod 600 ./agent-credential
```

The Agent credential is returned once. Store it in a mode-0600 file or a
secret manager, never in arguments or source control. Private targets remain
denied unless explicitly allow-listed:

```bash
XISNOVE_URL="$XISNOVE_URL" \
XISNOVE_AGENT_CREDENTIAL_FILE=./agent-credential \
XISNOVE_AGENT_ALLOWED_PRIVATE_CIDRS='10.0.0.0/8,192.168.0.0/16' \
go run ./agent/cmd/xisnove-agent
```

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
