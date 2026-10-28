# osac-metering chart

Deploys the OSAC metering service, the echo and M360 provider adapters,
and the rule revision 0001 ConfigMap with the admission policy that
protects it.

## Values

See `values.yaml`. The notable groups:

- `heartbeat.interval` — the round interval, default `60s`.
- `echoAdapter.enabled`, `m360Adapter.enabled` — the provider adapters.
- `database.connection` — the projected secret holding the database URL.

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
