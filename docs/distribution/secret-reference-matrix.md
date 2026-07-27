# Secret and bootstrap reference matrix

Xisnove v1 consumes secrets through references or workload-readable files. It does
not place secret values in command-line arguments, rendered manifests, logs,
OCI metadata, or release archives. Kubernetes Secret volumes, External Secrets Operator,
Vault Agent, the OpenBao injector, and CSI providers may all
materialize the same file contract; Xisnove does not require a provider-specific
client in v1.

| Consumer | Material | Production input | Rotation and ownership |
|---|---|---|---|
| `xisnove-server` | database URL and embedded credentials | `--database-url-file`; `--database-url` is raw/development compatibility | raw owner-only `0600` or workload-group `0440`; never argv or rendered environment for remote profiles |
| `xisnove-server` | managed Turso authentication token | `--database-auth-token-file` | same safe file contract; independently rotatable from the database URL |
| `xisnove-server` | cursor HMAC key | `--cursor-signing-key-file` or `XISNOVE_CURSOR_SIGNING_KEY_FILE` | read-only file, at least 32 bytes; replacement requires a bounded server restart |
| `xisnove-server` | notification keyring | `--notification-master-key-file` or `XISNOVE_NOTIFICATION_MASTER_KEY_FILE` | read-only versioned JSON keyring; rotation is an explicit resumable command |
| `xisnove-server admin bootstrap` | administrator password | `--password-file`; email is non-secret | owner-readable file or stdin wrapper; bootstrap is create-once and idempotent |
| `xisnove-ui` | cookie HMAC secret | `XISNOVE_UI_COOKIE_SECRET_FILE` | read-only file containing base64 for at least 32 decoded bytes; direct environment value is development compatibility only |
| `xisnove-operator` | provisioning credential | `--provisioning-credential-file` or `XISNOVE_PROVISIONING_CREDENTIAL_FILE` | mounted read-only Secret file; reread on requests so atomic projection updates take effect |
| `xisnove-agent` | Agent credential bundle | `XISNOVE_AGENT_CREDENTIAL_FILE` | raw installs use owner-only `0600`; operator projection uses explicit UID/GID/FSGroup `101`, mode `0440`, atomic projection, and generation metadata |

Workload file readers follow Kubernetes projected-Secret symlinks, validate
the opened descriptor target is regular, and reject world access plus group
write or execute; raw owner-only `0600` remains valid. All readers bound file
size and redact paths from public diagnostics. Kubernetes resources refer
to existing Secret names and keys. Compose and raw resources refer to host
paths. They never accept a secret literal in committed configuration.

## Bounded first-install sequence

1. Run the explicit expand migration with a bounded lock timeout.
2. Bootstrap the administrator from the mounted password file. Repeating the
   command with the same administrator succeeds without changing the password;
   conflicting identity fails closed.
3. Start the API and wait for `/readyz`.
4. Run `xisnove-agent enroll` with the one-time token file. It durably journals
   a caller-generated credential and idempotency key before the request; server
   replay returns the same Agent and credential after a lost response.
5. Materialize the credential bundle once with `0600` for raw installs or the
   explicit workload-only Kubernetes projection above. Retries reuse the same
   caller credential and generation and never request an implicit rotation.
6. Start the Agent, then wait for Agent readiness and heartbeat observation.

Every step has a timeout and an idempotency identity. Restarting after any
completed step resumes at the next observation. Rotation is always a separate,
audited operation.

## Provider extension boundary

Future direct integrations may add a `SecretReferenceResolver` adapter for
Vault, OpenBao, ESO status, or a CSI provider. The application-facing value is
still a named reference that resolves to bytes plus a version; deployment
artifacts and core application services remain provider-neutral. V1's file
contract is the compatibility surface those adapters must preserve.
