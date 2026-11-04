# osac-metering chart

Deploys the OSAC metering service, the echo and M360 provider adapters,
and the rule revision 0001 ConfigMap with the admission policy that
protects it.

## Values

See `values.yaml`. The notable groups:

- `heartbeat.interval` — the round interval, default `60s`.
- `readers` — outside systems allowed to read every tenant's heartbeat topics.
- `echoAdapter.enabled`, `m360Adapter.enabled` — the provider adapters.
- `database.connection` — the projected secret holding the database URL.

## Reader grants

A reader is an outside billing system that reads every tenant's heartbeat
topics, `osac.metering.heartbeat.<tenant>`, with one credential. Add one entry
per reader to `readers` when the chart is installed standalone:

```yaml
readers:
  - name: billing-reader
```

Through the umbrella installer chart, set the same list at
`metering.readers`:

```yaml
metering:
  readers:
    - name: billing-reader
```

The entry names the reader and no tenant. The chart renders one Strimzi
`KafkaUser` per entry with a read and describe ACL on the
`osac.metering.heartbeat.` topic prefix and a read ACL on a consumer group named
after the reader. The prefix covers every tenant, including tenants created
after the grant, so onboarding a tenant needs no chart change and hands over no
new Secret. The user gets nothing else: it cannot write, cannot read the raw
event log or a dead-letter topic. The reader name is also its consumer group,
which is the name Kafka Exporter reports lag under. The reader lists the
heartbeat topics by prefix at startup; a tenant created after the reader starts
is read on its next restart.

Reader names must be unique; a repeated name fails the render. Names that start
with `osac-` or `osac.` are reserved for OSAC, as are the KafkaUsers this chart
creates (`osac-metering` and its adapters): a collision would let a reader
replace an OSAC user's ACLs or join an adapter's consumer group. The umbrella
chart additionally rejects `fulfillment-service`, the KafkaUser it creates for
the fulfillment service.

The list is set through Helm values. The Enclave Wizard's schema-to-control
mapping covers enums, booleans, strings, and numbers only, so it renders no
control for this list of objects; that is the deliberate decision, and there is
no Wizard field for it. Flattening the list into per-reader scalar keys would
fit the mapping, but every grant would then need several keys kept in step, and
a mistake would grant the wrong reader. The list stays values-only.

Grant or revoke a reader by applying the values again with the installer. The
metering Deployment does not depend on these values, so nothing restarts. On
revocation Strimzi deletes the user, its password Secret, and its ACLs; the
running reader fails its next fetch rather than waiting to be told, and it loses
access to every tenant at once. Offboarding a tenant stops new usage, but its
heartbeat topics and retained records stay, so every reader can still read that
tenant's history with the same credential.

One credential reads every tenant, so a leaked reader Secret exposes all of
them. The Secret exists once per reader, in the Kafka namespace, and is named
after the reader. Hand its password, the shared OSAC CA, and the bootstrap
address to the reader's owner out of band; the chart writes none of them into
the repository. There is no per-tenant Secret and no per-tenant revocation:
stopping the reader stops every tenant.

To see how far behind a consumer is, enable `kafka.exporter.enabled` in the
`osac-infra` release. That value enables Strimzi's Kafka Exporter, its
PodMonitor, and the `MeteringConsumerLag` alert. The alert fires when any
consumer group reading a heartbeat topic is more than
`kafka.exporter.lagThreshold` records behind for 15 minutes (default 15) and
its lag has not fallen in that time. It covers the external readers and the
metering adapters, because all of them read the heartbeat topics.

The exporter reports one lag series per heartbeat topic under the group, so the
alert names the group and the topic that are behind. A metered resource sends
about one heartbeat per minute, so a single-VM tenant grows lag by about one
record per minute: the default catches a stalled consumer in about half an
hour. Busy tenants cross it sooner. Raise the threshold if a consumer may fall
further behind, or lower it for a small tenant. A consumer that has never
consumed has no consumer group and no lag series, so this alert cannot fire for
it; check that the group exists before relying on the alert. The rule lives in
the Kafka namespace, where the exporter series live, because user workload
monitoring evaluates a rule against its own namespace.

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
