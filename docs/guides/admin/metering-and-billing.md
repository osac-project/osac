# Metering and billing

**Audience:** the administrator who runs OSAC and connects it to a billing
system.

OSAC measures tenant usage and publishes it to Kafka, the message log that
carries the events. A tenant is one customer's isolated part of OSAC. Your
billing system reads the events, applies prices, and issues invoices. For the
Kafka settings metering needs, see the
[Kafka configuration](../installation/kafka-configuration.md) guide.

## What metering publishes

Metering runs a round every 60 seconds by default. In each round it sends one
usage event for each billable resource and meter, with the quantity since the
previous event. A meter is what metering measures, such as a running VM's
seconds or a volume's provisioned capacity. Metering covers six resource types
with seven meters. A running VM sends one `compute` event; a bare-metal host can
send `allocation` and `consumption`. A resource outside a billable state sends
nothing.

Each event covers one interval and carries one signed quantity: positive for
usage that happened, negative for a correction that takes usage back.

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
access to every billable tenant's heartbeat events. Each entry in
`metering.readers` is one reader, and one reader covers every billable tenant. A
second billing system is a second entry. Add its name to the list in the values
file for the `osac` release, the installer values:

```yaml
metering:
  readers:
    - name: billing-reader
```

The entry has one field, `name`. The grant covers the whole topic prefix, so a
new tenant needs no chart change. Apply the values with the installer; see the
[Helm deployment guide](../installation/helm-deployment-guide.md).

The chart, the Helm package that installs OSAC, creates one Strimzi `KafkaUser`
for each entry. Strimzi runs Kafka, and a `KafkaUser` is one Kafka account with
its own credentials. Each user gets this access:

| Access | Scope |
|---|---|
| Read and describe | Topics whose name starts with `osac.metering.heartbeat.` |
| Read | Consumer group named after the reader (`billing-reader` in the example) |
| Denied | `osac.metering.heartbeat.system` and `osac.metering.heartbeat.shared` |

A consumer group stores how far a reader has read for each topic partition.
Kafka calls that position the group's offset. Each heartbeat topic has one
partition, so the group stores one position per tenant. One grant gives one
credential and one group, named after the reader. The credential is read-only.

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
`shared` and the shared MaaS inference topic `osac.metering.inference` are
outside the grant.

Offboarding a tenant after its final invoice deletes its topics and the usage
records retained in them. Stopping the reader cuts off every tenant.

Keep the password in the reader's own secret store. Restrict the network path to
the broker. To rotate the password, revoke the reader and grant it again. Audit
who holds it. A reader groups usage by `tenant_id`.

### Watch the consumer's lag

Lag is the number of records a consumer still has to read. The alert is opt-in.
Enable `kafka.exporter.enabled` in the `osac-infra` release. The setting enables
Strimzi's Kafka Exporter, a PodMonitor that tells Prometheus where to scrape it,
and the `MeteringConsumerLag` alert.

The rule checks two things: lag above `kafka.exporter.lagThreshold` (default 15
records) and a 15-minute change that has not fallen. That condition must hold
for another 15 minutes, so a stalled consumer shows up in about half an hour. A
consumer that is catching up stays quiet, even while it is far behind. The
series names the group and the topic, so growing lag on one topic shows which
tenant is behind.

The alert covers the external readers and the metering adapters, the OSAC
programs that deliver events to billing systems. It covers heartbeat topics
only; `osac.metering.inference` is outside it. A consumer that never connected
has no lag series, so this alert cannot detect it. Check that the group exists
after the first run. The PodMonitor and the alert need the Prometheus operator
CRDs.

## Read the events

Sum every usage event that shares one key: `tenant_id`, `resource_type`,
`resource_id`, meter, component, and billing dimensions. Resource is the type
and id together; dropping either merges different objects. The component is the
billable line the meter feeds, and it stays in the key even when it equals the
meter name.

A correction is one of those events with a negative quantity, so the sum is the
billable total. Match a correction to the usage it corrects by the same key. A
correction is built in a later round, so it has its own id. The quantity, id,
and interval can differ from the event it corrects.

| Property | What it means for a reader |
|---|---|
| Topics | One per tenant, named `osac.metering.heartbeat.<tenant>` |
| Partitions | One per topic, so Kafka stores the records in one ordered log |
| Delivery | At least once; a repeat carries the same CloudEvent `id` |
| Deduplication | Drop an `id` you have already seen; a repeat carries the same id |
| Retention | How long Kafka keeps records. `topics.retentionMs`, a metering chart value, defaults to `-1`, which keeps every record |
| Compaction | Kafka keeps only the latest record for each key. Off; metering refuses a compacted topic |
| Retries and failures | The reader retries and handles failures |

Retention also sets metering's recovery window. A database restore can re-read
the records Kafka still holds. A finite retention bounds how far back a restore
can go.

Each event states its schema version, the version of the field layout it uses. A
published version never changes. A new field becomes a new version with its own
effective date. The reference lists every version. When a reader meets an
unknown CloudEvent type, it should alert and keep the event in the topic. The
example stops with an error and leaves the event uncommitted, so the next run
reads it again.

## Billing integration

OSAC measures. The billing system prices, applies taxes and discounts, and
issues invoices. Each event carries quantities and dimensions. Rates stay in the
billing system.

Two paths connect them:

- Use the M360 adapter when your billing system is M360. Set
  `metering.m360Adapter.enabled` to `true` and configure its API URL and key
  Secret; the chart ships the adapter and keeps it off until you enable it. It
  reads the heartbeat topics and the shared MaaS inference topic
  `osac.metering.inference`, so it bills both. A reader built from
  `metering.readers` covers heartbeat topics only.
  [OSAC-3784](https://redhat.atlassian.net/browse/OSAC-3784) tracks the rest of
  the billing work: pricing plans, cost models, and draft invoices.
- Build your own reader when you need another billing system. Start from the
  worked example below.

### External billing account id

A tenant can carry its account id from your billing system. Set it on the tenant
as `spec.external_billing_id` through the Fulfillment API, OSAC's management
API. The field takes at most 256 characters. The Fulfillment API shows it to
cloud provider admins; tenant users do not see it.

Metering copies the current value onto each new usage event as
`external_billing_account_id`. Any reader granted the heartbeat topics can read
the copied value. The field is absent when the tenant has no id. Treat the value
as a routing label, a value the billing system uses to route the charge. Events
already published keep the value they carried.

## Worked example

The `billing-reader` example is a Go program. It reads the heartbeat topics,
drops repeated ids, sums each key, and prints the billable totals. Its README
has the grant, the build, the Job manifest, and the commands:

- [Billing reader example](../../../osac-metering/adapters/examples/billing-reader/README.md)

To run it:

1. Add `billing-reader` to `metering.readers` and apply the installer values.
2. Copy the `ca-bundle` ConfigMap into the Kafka namespace, build the example
   image, and apply its Job there.
3. Read the Job log. The log is a delta, not a lifetime total. A first run with
   no committed offset prints all retained history; later runs print only the
   records after the committed offset. Do not add logs together or invoice one
   log as a lifetime total.

Any principal with `pods/log` access in the Kafka namespace can read the log.
Restrict that access, or send the totals to a controlled billing destination in
a production reader.

The example's unit tests use a fixture that totals 110 seconds of `compute` for
`vm-7`.

The example is a starting point for a production reader. It lists the heartbeat
topics once at startup, so a tenant created after it starts is read on the next
restart. It keeps its seen ids in memory, so a restart forgets them. It commits
its offsets to the consumer group after the bill prints. A production reader
stores its seen ids durably and re-lists or watches for new topics.
