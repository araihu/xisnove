# Kubernetes edge operations

The contract-independent chart is under `charts/xisnove-edge`. Full deployment
must wait for the frozen SDK adapter recorded in `operator/INTEGRATION.md`.

## Secret inputs

The chart requires an existing Secret containing a narrowly scoped operator
provisioning credential. It mounts the Secret as a file; the credential is not
placed in values, environment variables, CRD fields, events, or logs.

Vault Agent, OpenBao Agent, CSI drivers, and External Secrets Operator may
materialize that existing Kubernetes Secret. Xisnove v1 does not call any of
their APIs. This `existingSecret` boundary keeps future providers replaceable.

The operator creates each Agent credential Secret named by
`spec.credentialSecretRef`. The Agent mounts the complete Secret volume and
rereads the credential file. Its read-only ServiceAccount has no permission to
fetch Secret objects through the Kubernetes API.

## Explicit overlap-safe rotation

Scheduled rotation is intentionally absent in v1. To rotate, increment
`spec.credentialRotation.requestedGeneration` by one:

1. The operator idempotently requests the exact new generation while the old
   one remains valid.
2. It atomically updates the mounted Secret, keeping the old value under an
   internal overlap key.
3. Kubelet updates the mounted Secret directory and the Agent rereads the
   current credential file.
4. The operator waits until the control plane reports a heartbeat using the
   new generation.
5. It revokes the old generation and removes the overlap value.

Retries use a stable generation-specific idempotency key. A crash between
remote issuance and Secret update must return the same generation on replay.

## Stale discovery

`DiscoveryFresh=False,Reason=Stale` means the last complete catalog snapshot
is older than `spec.discovery.staleAfterSeconds`. It is an observation-quality
condition, not an alert. Existing promoted Monitors continue unchanged.

## Finalizer recovery

Normal deletion waits for an ownership-checked remote delete. If the control
plane is permanently gone, annotate the object before retrying deletion:

```sh
kubectl annotate monitor NAME monitoring.xisnove.io/force-delete=true
```

The same annotation applies to `Agent`. Force removal can leave a remote
orphan and should be reserved for disaster recovery.
