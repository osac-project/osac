# Metering and billing

**Audience:** the administrator who runs OSAC and connects it to a billing
system.

OSAC measures tenant usage and publishes it to Kafka, the message log that
carries the events. A tenant is one customer's isolated part of OSAC. Your
billing system reads the events, applies prices, and issues invoices. For the
Kafka settings metering needs, see the
[Kafka configuration](../installation/kafka-configuration.md) guide.

## What metering publishes

By default, metering sends one usage event for each billable meter every 60 seconds. A meter
is what metering measures, such as a running VM's seconds or a volume's
provisioned capacity. Metering covers six resource types with seven meters. Each
event covers one interval and carries one signed quantity: positive for usage
that happened, negative for a correction that takes usage back.

Events go to the tenant's topic. A topic is a named, append-only log in Kafka.
The topic is `osac.metering.heartbeat.<tenant>`. Each event is a CloudEvent, a
standard wrapper with headers, a JSON body, and a unique `id`.

The contract reference lists every resource type, meter, unit, billable state,
billing dimension, and event field. It also states the reading rule. A billing
dimension is a name and value pair, such as an instance type.

- [Metering usage event contract](../../../osac-metering/docs/contract/reference.md)

CI regenerates the reference from the metering code and fails on a stale copy.

Metering keeps three things internal: the raw event log `osac.events.<tenant>`,
the dead-letter topics that hold rejected events, and its database. Only
metering's own components read them.

## Grant a reader

A reader is an outside system with its own Kafka credentials and read-only
access to every billable tenant's heartbeat events. One reader covers every
tenant. Add its name to the `metering.readers` list in the values file for the `osac` release, the installer values:

```yaml
metering:
  readers:
    - name: billing-reader
```

The entry has one field, `name`. The grant covers the whole topic prefix, so a
new tenant needs no chart change. Apply the values with the installer; see the
[Helm deployment guide](../installation/helm-deployment-guide.md).

The chart, the Helm package that installs OSAC, creates one Strimzi `KafkaUser`.
Strimzi runs Kafka, and a `KafkaUser` is one Kafka account with its own
credentials. It gets this access:

| Access | Scope |
|---|---|
| Read and describe | Topics whose name starts with `osac.metering.heartbeat.` |
| Read | Consumer group named after the reader (`billing-reader` in the example) |
| Denied | `osac.metering.heartbeat.system` and `osac.metering.heartbeat.shared` |

A consumer group is a named bookmark in a topic. Kafka records how far the
reader has read at that bookmark, and calls the position the group's offset. One
grant gives one credential and one group, named after the reader. The credential
is read-only.

The reader finds tenants by listing topics with the prefix, or by subscribing to
the regular expression `^osac\.metering\.heartbeat\..*`. A subscriber skips the
two platform topics.

Reader names are unique lowercase DNS names of at most 253 characters. OSAC
reserves the `osac-` and `osac.` prefixes, and the `fulfillment-service`
KafkaUser name.

For every chart value, see the
[metering chart README](../../../osac-metering/charts/osac-metering/README.md).

Hand the reader three values separately: the broker's bootstrap address, the
shared OSAC CA certificate, and its password. The bootstrap address is the host
and port the reader connects to. Strimzi writes the password into a Kubernetes
Secret named after the reader, in the Kafka namespace. Keep all three outside
the repository.

To grant a reader, add the entry and apply the installer values again.

To revoke it, remove the entry and apply them again. Strimzi deletes the
KafkaUser, its password Secret, and its permissions. No OSAC service restarts.
New connections fail.

A reader that is already connected is denied on its next fetch once the broker
applies the removal. It can finish the records it has already fetched. To stop
it at once, stop the reader process. One removal blocks new access to every
tenant at once.

### One credential reads every tenant

One credential reads every billable tenant's heartbeat events, so a leaked
credential exposes every tenant's usage. The platform tenants `system` and
`shared` and the shared inference topic are outside the grant.

To cut off one tenant, stop the reader or offboard the tenant after its final invoice.

Keep the password in the reader's own secret store. Restrict the network path to
the broker. To rotate the password, revoke the reader and grant it again. Audit
who holds it. A reader groups usage by `tenant_id`.

### Watch the consumer's lag

Lag is the number of records a consumer still has to read. The alert is opt-in.
Enable `kafka.exporter.enabled` in the `osac-infra` release. The setting enables
Strimzi's Kafka Exporter, a PodMonitor that tells Prometheus where to scrape it,
and the `MeteringConsumerLag` alert.

The alert fires when a consumer group reading a heartbeat topic stays more than
`kafka.exporter.lagThreshold` records behind for 15 minutes. A consumer that is
catching up stays quiet: the alert also checks that lag has not fallen in that
time. The series names the group and the topic, so growing lag on one topic
shows which tenant is behind.

The alert covers the external readers and the metering adapters, the OSAC
programs that deliver events to billing systems. A consumer that has never
consumed has no group and no lag series. Check that the group exists after the
first run. The PodMonitor and the alert need the Prometheus operator CRDs.

## Read the events

Sum every usage event that shares one key: tenant, resource, meter, component,
and billing dimensions. A correction is one of those events with a negative
quantity, so the sum is the billable total.

Match a correction to the usage it corrects by the same key. A correction is
built in a later round, so it has its own id. The quantity, id, and interval can
differ from the event it corrects.

| Property | What it means for a reader |
|---|---|
| Topics | One per tenant, named `osac.metering.heartbeat.<tenant>` |
| Partitions | One per topic, so Kafka stores the records in one ordered log |
| Delivery | At least once; a repeat carries the same CloudEvent `id` |
| Deduplication | Drop an `id` you have already seen; a repeat carries the same id |
| Retention | How long Kafka keeps records. `topics.retentionMs`, a metering chart value, defaults to `-1`, which keeps every record |
| Compaction | Kafka keeps only the latest record for each key. Off; metering refuses a compacted topic |
| Retries and failures | The reader retries and handles failures |

Retention also sets metering's recovery window. With indefinite retention, a new
reader that starts at the beginning sees the tenant's whole history. A database
restore can re-read the same records. A finite retention bounds how far back a
restore can go.

Each event states its schema version, the version of the field layout it uses. A
published version never changes. A new field becomes a new version with its own
effective date. The reference lists every version. When a reader meets an
unknown CloudEvent type, it should alert and keep the event.

## Billing integration

OSAC measures. The billing system prices, applies taxes and discounts, and
issues invoices. Each event carries quantities and dimensions. Rates stay in the
billing system.

Two paths connect them:

- Use the M360 adapter when your billing system is M360.
  [OSAC-3784](https://redhat.atlassian.net/browse/OSAC-3784) tracks the billing
  work: the adapter path, pricing plans, cost models, and draft invoices. The
  metering chart ships the M360 adapter.
- Build your own reader when you need another billing system. Start from the
  worked example below.

### External billing account id

A tenant can carry its account id from your billing system. Set it on the tenant
as `spec.external_billing_id` through the Fulfillment API, OSAC's management
API. The field takes at most 256 characters. Only cloud provider admins read it.

Metering copies the current value onto each new usage event as
`external_billing_account_id`. The field is absent when the tenant has no id.
Treat the value as a routing label, a value the billing system uses to route the charge. Events already published keep
the value they carried.

## Worked example

The `billing-reader` example is a Go program. It reads the heartbeat topics,
drops repeated ids, sums each key, and prints the billable totals. Its README
has the grant, the build, the Job manifest, and the commands:

- [Billing reader example](../../../osac-metering/adapters/examples/billing-reader/README.md)

To run it:

1. Add `billing-reader` to `metering.readers` and apply the installer values.
2. Copy the `ca-bundle` ConfigMap into the Kafka namespace, build the example
   image, and apply its Job there.
3. Read the Job log. It prints the totals the cluster has published.

The example's unit tests use a fixture that totals 110 seconds of `compute` for
`vm-7`.

The example is a starting point for a production reader. It lists the heartbeat
topics once at startup, so a tenant created after it starts is read on the next
restart. It keeps its seen ids in memory, so a restart forgets them. It commits
its offsets to the consumer group after the bill prints. A production reader
stores its seen ids durably and re-lists or watches for new topics.
