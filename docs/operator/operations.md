# Kubernetes edge operations

The contract-independent chart is under `charts/xisnove-edge`. Full deployment
must wait for the frozen SDK adapter recorded in `operator/INTEGRATION.md`.

The edge installation is intentionally not the monitoring control plane. Run
the relational control plane on an external VPS, cloud service, or separate
cluster, then configure the operator and Agent with its public API URL. The
kind acceptance journey enforces this topology with a SQLite-backed server in
a separate Docker container and only the operator and Agent inside Kubernetes.

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
The previous credential is revoked only after the server reports a heartbeat
authenticated by the new generation. Operator or Agent restarts and API
partitions may lengthen the overlap, but cannot move revocation ahead of that
fence.

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

## Reproducible edge verification

Run the complete black-box journey with pinned images and local tools:

```sh
make kind-edge-e2e
```

The harness installs the chart with Helm and drives the public generated Go
SDK plus `kubectl`. It verifies discovery, explicit promotion, Secret RBAC,
interrupted credential rotation, network loss, independent process restarts,
and recreated-UID ownership refusal. Successful runs delete their exact kind
cluster, external server container, SQLite volume, images, and temporary
credential files. Failure artifacts contain workload resources, logs, and
Secret names/types/key names only; Secret values are never serialized.
