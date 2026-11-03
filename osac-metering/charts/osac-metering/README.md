# osac-metering chart

Deploys the OSAC metering service, the echo and M360 provider adapters,
and the rule revision 0001 ConfigMap with the admission policy that
protects it.

## Values

See `values.yaml`. The notable groups:

- `heartbeat.interval` — the round interval, default `60s`.
- `readers` — outside systems allowed to read one tenant's usage events.
- `echoAdapter.enabled`, `m360Adapter.enabled` — the provider adapters.
- `database.connection` — the projected secret holding the database URL.

## Reader grants

A reader is an outside billing system that reads one tenant's usage events
with its own credentials. Add one entry per reader to `readers`:

```yaml
readers:
  - name: billing-reader
    tenant: acme
```

The chart renders one Strimzi `KafkaUser` per entry. The user gets a read ACL
on `osac.metering.heartbeat.<tenant>` and a read ACL on a consumer group named
after the reader. It gets nothing else: it cannot write, cannot read the raw
event log or the dead-letter topic, and cannot read another tenant's topic.
The reader name is also its consumer group, which is the name Kafka Exporter
reports lag under.

Reader names must be unique; a repeated name fails the render. `system` and
`shared` are rejected because metering never creates their usage topics. Names
that start with `osac-` or `osac.` are reserved for OSAC, as are the KafkaUsers
this chart creates (`osac-metering` and its adapters): a collision would let a
reader replace an OSAC user's ACLs or join an adapter's consumer group. The
umbrella chart additionally rejects `fulfillment-service`, the KafkaUser it
creates for the fulfillment service.

The list is set through Helm values. The Enclave Wizard's schema-to-control
mapping covers enums, booleans, strings, and numbers only, so it renders no
control for this list of objects; that is the deliberate decision, and there is
no Wizard field for it. Flattening the list into per-reader scalar keys would
fit the mapping, but every grant would then need several keys kept in step, and
a mistake would grant the wrong tenant. The list stays values-only.

Grant or revoke a reader by applying the values again with the installer. The
metering Deployment does not depend on these values, so nothing restarts. On
revocation Strimzi deletes the user, its password Secret, and its ACLs; the
running reader fails its next fetch rather than waiting to be told. Offboarding
a tenant means removing its reader entries in the same change that retires the
tenant; the records stay until the tenant's topics are deleted.

Rolling back to a chart version that predates `readers` fails while the value
is set: the older umbrella schema rejects unknown keys under `metering`
(`additionalProperties: false`). Remove `metering.readers` from the values,
roll back, and add it again when the chart supports it. The reader KafkaUsers
are deleted by that rollback, so also tell the reader's owner that access ends.

The reader needs the bootstrap address, the shared OSAC CA, and its
password from the Secret named after it in the Kafka namespace. Hand those
over out of band. The chart never writes them into the repository.

To see how far behind a reader is, enable `prometheusRule.enabled`: the
`MeteringReaderLag` alert fires when a reader group is more than
`prometheusRule.readerLagThreshold` records behind for 15 minutes (default 15).
A metered resource sends about one heartbeat per minute, so a single-VM tenant
grows lag by about one record per minute: the default catches a stalled reader
in about half an hour. Busy tenants cross it sooner. Raise the threshold if a
reader may fall further behind, or lower it for a small tenant. A reader that
has never consumed has no consumer group and no lag series, so this alert
cannot fire for it; check that the group exists before relying on the alert.
Strimzi's Kafka Exporter, enabled on the installer's Kafka resource and scraped
by a PodMonitor, supplies `kafka_consumergroup_lag`. The reader lag rule is
rendered into the Kafka namespace, where the exporter series live, because user
workload monitoring evaluates a rule against its own namespace.

## Rule revisions

Revisions are immutable ConfigMaps named `osac-metering-rules-<NNNN>`
holding complete meter definitions and an `effective_at` time. Rules change
by adding a new revision with a future `effective_at` and restarting
metering; never by editing an existing one. The admission policy
`osac-metering-protect-rules` denies deleting a revision, because metering
refuses to start when a revision it has used is missing.

## Escape hatch: removing a backdated revision

Metering refuses to start when a newly added revision would take effect in
the past, because recording it would change past usage. If a bad revision
was created with a past `effective_at` and metering is down, an operator
can remove it:

1. Remove the policy binding so the delete is allowed. The binding's name
   starts with the release name, so select it by label:

   ```sh
   kubectl delete validatingadmissionpolicybinding \
     -l app.kubernetes.io/component=metering
   ```

2. Delete the bad revision ConfigMap:

   ```sh
   kubectl delete configmap osac-metering-rules-000N
   ```

3. Reapply this chart. The helm upgrade recreates the policy binding, and
   metering starts again against the remaining revisions.

Use this only for a revision metering has never billed under: a revision
that already produced heartbeats must never be removed, or past usage can
no longer be reproduced exactly.

## Upgrading from the shared-topic pipeline

Earlier releases of this chart created shared `osac.metering.lifecycle`,
`osac.metering.heartbeat`, `osac.metering.corrections`, and
`osac.metering.dlq` topics. This chart no longer renders those
`KafkaTopic` resources, so `helm upgrade` deletes them, and the broker
deletes the topics and their records with them. The per-tenant topics the
current pipeline uses are not affected. Export anything you still need
from the old topics — especially unreplayed dead-letter records — before
upgrading.

## Uninstalling

`helm uninstall` succeeds and leaves the rule revisions, the
delete-protection policy, and its binding in place: revisions are the
record of what was billed under which rules, and all three resources
carry `helm.sh/resource-policy: keep`, so no routine uninstall destroys
that record. Removing an installation completely is an explicit operator
action. Delete the protection first, then the revisions, using the names
helm rendered for this release (by default
`<release>-osac-metering-protect-rules` and `osac-metering-rules-<NNNN>`):

```sh
kubectl delete validatingadmissionpolicybinding <release>-osac-metering-protect-rules
kubectl delete validatingadmissionpolicy <release>-osac-metering-protect-rules
kubectl delete configmap osac-metering-rules-0001
helm uninstall <release>
```

Deleting the namespace removes the revisions with it once the policy is
gone; the policy and binding are cluster-scoped and must be deleted
explicitly either way.
