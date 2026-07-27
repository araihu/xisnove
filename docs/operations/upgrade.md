# Upgrade and rollback

Upgrade only between published versions whose candidate manifest, detached
checksum, SBOMs, and license inventory verify. Back up the relational database
and deployment configuration first. Retain the prior release bundle and its
manifest until post-upgrade health checks pass.

1. Verify the candidate in a clean directory with `verify-bundle.sh`.
2. Read compatibility and database-profile notes for the target version.
3. Back up SQLite or local Turso with the documented quiesced procedure, or
   obtain a provider-consistent PostgreSQL or managed Turso backup.
4. Stop probe scheduling or drain Agents where a check interruption matters.
5. Run the target `xisnove-server migrate` binary once. Never run migration
   concurrently against SQLite or local Turso.
6. Deploy server, UI, operator, and Agents from the same candidate version.
7. Confirm readiness, API version, lease flow, result ingestion, incident
   projection, notification outbox delivery, and UI/API reachability.

For Kubernetes, use Helm's atomic upgrade and retain the prior release:

```sh
helm upgrade --install xisnove ./xisnove-<version>.tgz \
  --namespace xisnove --create-namespace --atomic --wait
```

For Compose, replace image digests in the extracted deployment bundle, run the
one-shot migration service, then recreate application services. For raw or
systemd deployments, replace all five binaries and packaged resources as one
versioned unit before restarting services.

## Rollback

Application rollback is safe only while the previous binary supports the
current schema. Stop the failed version, restore the pre-upgrade database when
the migration is not backward-compatible, restore the previous configuration,
then deploy the retained prior candidate bytes. Do not point an older server at
a database after an irreversible migration. Confirm readiness and one complete
probe-to-incident-to-notification journey after rollback.
