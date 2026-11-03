# Metering and billing

**Audience:** Cloud Provider Admin.

OSAC measures tenant usage and publishes it to Kafka. Your billing system reads
those events, applies prices, and issues invoices. For the Kafka settings
metering needs, see
[Kafka configuration](../installation/kafka-configuration.md).

## What metering publishes

Metering covers six resource types with seven meters. A meter is what it
measures, such as a running VM's seconds. By default, every 60 seconds it sends
the usage since the last event for each billable resource. Each event covers one
interval and carries one signed quantity. A correction is a negative quantity
that takes back usage already sent.

Events go to the tenant's topic, `osac.metering.heartbeat.<tenant>`. Each event
is a CloudEvent: an envelope with a JSON payload and a unique `id`.

The contract reference lists every resource type, meter, unit, billable state,
billing dimension, and event field. It also states the reading rule.

- [Metering usage event contract](../../../osac-metering/docs/contract/reference.md)

CI regenerates the reference from the metering code and fails on a stale copy.
Link to it. Do not copy its tables into another document.

Metering keeps three things internal: the raw event log
`osac.events.<tenant>`, the dead-letter topics that hold rejected events, and
its database. A reader cannot read them.

## Grant a reader

A reader is a system outside OSAC with its own Kafka credentials. One reader
covers every tenant. Add its name to the `metering.readers` list in the
installer values:

```yaml
metering:
  readers:
    - name: billing-reader
```

The entry names the reader. It has no tenant field. The grant covers the topic
prefix, so a new tenant needs no chart change.

The chart renders one Strimzi `KafkaUser`. That is the Kafka account, and it
gets this access:

| Access | Scope |
|---|---|
| Read and describe | Topics whose name starts with `osac.metering.heartbeat.` |
| Read | Consumer group `billing-reader` |

A consumer group is a named position in the topic, where Kafka records the
reader's offsets. One grant gives one credential and one group, named after the
reader. The reader cannot write.

The reader finds tenants by listing topics with the prefix, or by subscribing
to the regular expression `^osac\.metering\.heartbeat\..*`.

Reader names must be unique. A name that starts with `osac-` or `osac.` is
rejected, because OSAC reserves those prefixes for its own users.

For every chart value, see the
[metering chart README](../../../osac-metering/charts/osac-metering/README.md).

Hand the reader three values separately: the broker's bootstrap address, the
shared OSAC CA certificate, and its password. Strimzi writes the password into
the Secret named after the reader in the Kafka namespace. The repository holds
none of them.

To grant a reader, add the entry and apply the installer values again. To
revoke it, remove the entry and apply them again. Strimzi deletes the user, its
password, and its permissions. No OSAC service restarts. A running reader fails
its next request within 60 seconds. One removal stops access to every tenant at
once.

### The trade

One credential reads every tenant's usage events. A leaked credential exposes
all of them. You cannot revoke one tenant without stopping the reader or
deleting that tenant's topics.

Keep the password in the reader's own secret store. Restrict the network path
to the broker. To rotate the password, revoke the reader and grant it again.
Audit who holds it. A reader keeps tenants apart by `tenant_id` and by
`external_billing_account_id`.

### Watch the reader's lag

Lag is the number of records a reader has not read yet. Enable
`prometheusRule.enabled` on the metering chart. Strimzi's Kafka Exporter reports
lag per heartbeat topic, and the `MeteringReaderLag` alert fires when the group
is more than `prometheusRule.readerLagThreshold` records behind for 15 minutes.
Growing lag on one topic shows which tenant is behind. A reader that has never
consumed has no group and no lag series, so check that the group exists after
the first run.

## Read the events

Add every usage event for one key: tenant, resource, meter, component, and
billing dimensions. A billing dimension is a name and value pair, such as an
instance type. A correction is a negative event for a key already sent, so
adding it gives the billable quantity.

Match a correction to the usage it corrects by tenant, resource, meter,
component, and billing dimensions. Never match by quantity, event id, or
interval. A correction is built in a later round, so it never shares the id of
the event it corrects.

| Property | What it means for a reader |
|---|---|
| Topics | One per tenant, named `osac.metering.heartbeat.<tenant>` |
| Partitions | One per topic, so Kafka stores the records in one ordered log |
| Delivery | At least once; a repeat carries the same CloudEvent `id` |
| Deduplication | Drop an `id` you have already seen, and never recompute it |
| Retention | `topics.retentionMs`, default `-1`, which keeps every record |
| Compaction | None; metering refuses a compacted topic |
| Retries and failures | The reader owns both; OSAC does not retry for a reader |

Delivery is at least once. A repeat carries the same `id`, so a reader must
drop ids it has seen before summing.

Retention is how long Kafka keeps records. It also sets metering's recovery
window. With indefinite retention, a new reader that starts at the beginning
sees the tenant's whole history. A rebuild after a restore reads the same
records. A finite retention bounds how far back a rebuild can go.

Each event names its schema version, and a published version never changes. A
new field becomes a new version with its own effective date. The reference lists
every version. If a reader meets an unknown CloudEvent type, it should alert and
keep the event.

## Billing integration

OSAC measures. The billing system prices, applies taxes and discounts, and
issues invoices. The contract carries quantities and dimensions. Rates stay in
the billing system.

Two paths connect them:

- Use an OSAC adapter when your billing system is M360 or Red Hat Cost
  Management. [OSAC-3784](https://redhat.atlassian.net/browse/OSAC-3784) covers
  the billing work: the adapter path, pricing plans, cost models, and draft
  invoices. The metering chart ships the M360 adapter.
- Build your own reader when no adapter exists for your billing system. Start
  from the worked example below.

### External billing account id

A tenant can carry its account id from your billing system. Set it on the tenant
as `spec.external_billing_id` through the private Fulfillment API. The field
takes at most 256 characters. Tenant users cannot read it.

Metering copies the current value onto each new usage event as
`external_billing_account_id`. The field is absent when the tenant has no id.
Treat the value as a routing label for the charge. Changing it does not rewrite
events already published.

## Worked example

The `billing-reader` example is a small Go program. It reads the heartbeat
topics, drops repeated ids, sums each key, and prints the billable total. Its
README has the grant, the build, the Job manifest, and the commands:

- [Billing reader example](../../../osac-metering/adapters/examples/billing-reader/README.md)

To run it:

1. Add `billing-reader` to `metering.readers` and apply the installer values.
2. Build the example image and apply its Job in the Kafka namespace.
3. Read the Job log. With the example data, it prints 110 seconds of `compute`
   for `vm-7`.

The example is a starting point. It keeps deduplication state in memory, so a
restart forgets the ids it has seen. It reads one topic at a time. A production
reader stores its offsets and seen ids durably and reads every topic under the
prefix.
