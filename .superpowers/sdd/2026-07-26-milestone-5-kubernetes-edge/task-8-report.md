# Task 8 report: controller Secret lifecycle and bounded status

## Delivered

- Replaced the stale controller use of the retired credential-issuing boundary
  with the frozen caller-supplied credential API.
- Generates 32 crypto-random bytes locally and writes only JSON credential
  bundles to the Agent-owned Secret. The state machine is `next -> current ->
  previous`: `credential.next` is first unmounted, remote apply/PUT succeeds,
  then promotion exposes exactly one current bundle. The Deployment Secret
  volume projects only that current key, never staged or previous data.
- Retains prior material until a replacement-generation heartbeat is observed,
  then performs idempotent revoke before deleting the prior Secret key. A
  Secret-write crash after apply, PUT, or revoke converges by reusing the
  already-staged bundle rather than minting another credential.
- Uses the uncached `APIReader` seam for exact Secret reads, refuses a Secret
  owned by any different Agent UID, and uses a stable kind/namespace/name owner
  key plus a distinct UID in every control-plane mutation. Agent finalization
  supports owner-only remote deletion when status lost the external ID.
- Bounds condition messages to 256 bytes and condition lists to eight entries.
  Monitor health maps pending/unknown to `Degraded=Unknown`, up to False, and
  degraded/down to True while Ready and Synced remain reconciliation outcomes.
  Agent workload status now uses actual Deployment observed generation and
  available replicas; heartbeat and discovery freshness use configurable
  reconciler and existing discovery thresholds.

## RED evidence

The supplied baseline controller package did not compile against the frozen
Task 5 boundary:

```text
unknown field NeedsCredential in struct literal of type controlplane.ApplyAgentRequest
state.Credential undefined (type controlplane.AgentState has no field or method Credential)
r.ControlPlane.IssueAgentCredential undefined
```

The first new truth-table test was added before its helper and the focused
command failed while that obsolete lifecycle was still present. During the
initial staging green loop, the new test exposed that `credential.next` was not
being selected as the generation-one apply bundle:

```text
initial credential staging did not produce a registered Agent
```

`initialBundle` now uses the staged generation-one bundle for the replay-safe
initial apply.

## GREEN evidence

```text
$ GOWORK=off go -C operator test ./internal/controller
ok github.com/araihu/xisnove/operator/internal/controller

$ GOWORK=off go -C operator test -race ./internal/controller ./api/...
ok github.com/araihu/xisnove/operator/internal/controller
?    github.com/araihu/xisnove/operator/api/v1alpha1 [no test files]

$ make -C operator generate
./hack/update-codegen.sh

$ make -C operator verify
./hack/verify-codegen.sh

$ git diff --check
(exit 0)
```

## Regression coverage

- health truth table and bounded condition count/message;
- initial next-stage retry, including a failed remote apply;
- post-apply/pre-promotion Secret write crash;
- PUT failure retaining the same unmounted replacement bundle;
- rotation promotion, replacement heartbeat, revoke, and post-revoke Secret
  write crash with idempotent retry;
- old-UID Secret adoption refusal and owner-only Agent finalization;
- actual Deployment generation/availability, stale heartbeat, and stale
  complete discovery conditions.

## Self-review and scope

Reviewed the owned diff for plaintext/status exposure, Secret projection,
owner UID separation, mutation idempotency, crash recovery, and status bounds.
No plaintext credential is written to status, conditions, logs, or Deployment
environment. `operator/api` generated code was run and has no semantic diff:
the existing CRD schema is unchanged, so chart/CRD and manager wiring remain
for their explicitly later task. No push was performed.
