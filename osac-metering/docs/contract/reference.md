# OSAC metering usage event contract

This contract covers the usage events OSAC metering publishes: 6 resource types and 7 meters, as rule revision 0001 defines them. The raw event log, the failed-record topics, and the usage database stay internal. Only the per-tenant usage topics and the events on them are part of this contract.

It is generated from the metering field registry, the default rule revision the chart ships, and the published schema files. Do not edit it by hand.

## Terms

| Term | Meaning |
|---|---|
| Usage event (heartbeat) | One measured quantity over one interval for one resource and meter, sent as a CloudEvent on the tenant's usage topic. |
| Resource | The billable object the usage is about, named by `resource_type` and `resource_id`. |
| Metering field registry | The fields metering tracks on each resource, declared by annotations on the private protos in `proto/metering` and kept in sync with fulfillment's database triggers by drift tests. |
| Meter | What is measured on a resource, for example a running VM's seconds. |
| Component | The billable line a meter feeds. It is the meter name unless a rule revision sets another. For example, the `compute` meter feeds the `compute` component, the line the provider prices. The running seconds are the quantity, in `usage.quantity`. |
| Billing dimensions | Name and value pairs that identify the priced variant of the usage, such as an instance type. A change re-keys the usage. |
| Interval | One span of time: `from` inclusive, `to` exclusive, in UTC with whole seconds. |
| Correction | A usage event with a negative quantity for the same resource, meter, component, and billing dimensions; it takes back usage already sent. |
| Reader | The provider's system that reads usage events. |
| Rule | A meter definition in a rule revision: the states that bill, the dimensions the meter carries, and the multiplier. |
| Rule revision | An immutable set of meter rules with an effective date. The chart ships the default revision. |
| Schema version | The published shape of a usage event, named by the suffix of its CloudEvent type, for example `v1`. |
| External billing account id | The tenant's id in the provider's billing system, carried by usage events when the tenant has one. The provider supplies the id and owns its correctness. M360 keeps its own tenant mapping, so this is the provider's identifier. |

## How to read usage

Add every usage event for one resource, meter, component, and set of billing dimensions. A correction is a usage event with a negative quantity for the same resource, meter, component, and billing dimensions; adding it takes back the usage it corrects. The sum is the billable quantity.

Match a correction to the usage it corrects by tenant, resource, meter, component, and billing dimensions. Never match by quantity, event id, or interval. A correction is sent in a later round, so it never shares the id of the event it corrects.

## Lifecycle

A resource starts billing when a rule revision says its state is billable. Each round, by default every `60s`, metering sends the difference since the last event for the same usage.

A state or dimension change re-keys the usage: later events for the resource carry the new dimensions, and each key is summed on its own.

When a resource stops being billable, metering sends a final correction for the span it billed past the stop.

To rebuild a billable quantity from retained records, add the records for the key. See Schema versions for how replay reads a record. The total matches what the reader saw.

## Transport

| Fact | The rule |
|---|---|
| Topic naming | One usage topic per tenant, named `osac.metering.heartbeat.<tenant>` |
| Ordering | Each usage topic has one partition, so events arrive in the order metering sent them |
| Delivery | At least once. A repeat carries the same CloudEvent `id` |
| Retention | Set per installation with `topics.retentionMs` (milliseconds); the default `-1` keeps every record |
| Compaction | None. Metering refuses a compacted usage topic and sets `cleanup.policy` to `delete` |
| Raw event log | Internal. Metering reads `osac.events.<tenant>` and never publishes it |
| Failed records | Internal. OSAC's adapters write rejected events to `osac.metering.dlq.<tenant>` |
| Retries | The reader owns its retries and its failed records. OSAC does not retry for a reader |

## Deduplication

The CloudEvent `id` is the deduplication key. It is a lower-case hex SHA-256 of the tenant, resource type, resource id, meter, component, billing dimensions, interval start, interval end, and heartbeat time. It leaves out the quantity and the billing account id (see Billing account id), so a resend carries the same id.

A reader drops an event whose id it has already seen and never recomputes the id.

## Schema versions

Every usage event names its schema version in the CloudEvent `type`: the suffix after the last dot, for example `v1` in `osac.resource.heartbeat.v1`.

A published schema never changes. A new field is a new version file with the next version and the date it takes effect. The old file stays, so its records stay readable.

Versions are add-only: a new field is added, and no field is removed or given a new type. Replay decodes every retained record with the current payload shape, so the current shape must stay a superset of every published version; that is what lets one reader read records written under older versions.

A version's effective date is the earliest date its events may carry it, measured by `heartbeat_time`, the round time that wrote the record. A reader compares the version's `effective_at` to `heartbeat_time` for the version boundary. The CloudEvent `time` is the interval start, which can be earlier than the round. Metering writes its current version unconditionally and never selects a version by date, so the version must not take effect after the release that ships it. Metering refuses to load a current version whose effective date is in the future; an older, future-dated version may wait in the tree until its release.

A published version stays readable for as long as its records are retained. Retention is indefinite by default, so no version has been retired.

| Version | CloudEvent type | Effective from | Source file | SHA-256 |
|---|---|---|---|---|
| `v1` | `osac.resource.heartbeat.v1` | 1970-01-01T00:00:00Z | `osac-metering/schema/versions/v1.json` | `6f691f271e178162b4b5c0ad4e0049ac69d68214951bb812b723ac2ec5602988` |

A reader that meets a type it has not adopted alerts, keeps the event, and adds support before that version's effective date. OSAC's adapters forward the CloudEvent type unchanged; they never relabel an event as another version.

### Event fields of version `v1`

#### CloudEvent attributes

| Field | Type | Required | Unit | Meaning |
|---|---|---|---|---|
| `specversion` | string, always `1.0` | required | - | CloudEvents specification version. Always 1.0. |
| `id` | string | required | - | Deduplication key: lower-case hex SHA-256 of the tenant, resource type, resource id, meter, component, billing dimensions, interval start, interval end, and heartbeat time. It leaves out the quantity and the external billing account id, so a resend carries the same id. |
| `type` | string, always `osac.resource.heartbeat.v1` | required | - | CloudEvents type. Always osac.resource.heartbeat.v1; the suffix is the schema version. |
| `source` | string, always `osac-metering` | required | - | Producer of the event. Always osac-metering. |
| `datacontenttype` | string, always `application/json` | required | - | Media type of the payload. Always application/json. |
| `time` | string | required | UTC, whole seconds | CloudEvents time. Equal to the interval start in data.usage.from. |
| `osacresourceid` | string | required | - | Resource id the usage belongs to. Equal to data.resource_id. |
| `osacresourcetype` | string | required | - | Resource type value the usage belongs to. Equal to data.resource_type. |
| `osactenant` | string | required | - | Tenant the usage belongs to. Equal to data.tenant_id. |

#### Payload

| Field | Type | Required | Unit | Meaning |
|---|---|---|---|---|
| `tenant_id` | string | required | - | Tenant the usage belongs to. |
| `resource_type` | string | required | - | Resource type value the usage belongs to, for example cluster_order. The resources section lists every value. |
| `resource_id` | string | required | - | Id of the resource the usage belongs to. |
| `meter` | string | required | - | Meter name from the default rule revision, for example compute. |
| `component` | string | required | - | Billable line the meter feeds. It is the meter name unless the rule revision sets another. |
| `billing_dimensions` | object of string | required | - | Dimension name to value for the interval. Each event carries its meter's dimensions plus deployment, tenant, project, catalog, template, and meter_type. The meter's dimensions are listed in the Dimensions table of each resource section; the identity dimensions are listed under Resources and meters. |
| `usage` | object | required | - | One measured interval. From is inclusive and to is exclusive. |
| `usage.semantics` | string, always `interval` | required | - | Always interval: the quantity covers one interval and is never cumulative. |
| `usage.from` | string | required | UTC, whole seconds | Start of the interval, inclusive. |
| `usage.to` | string | required | UTC, whole seconds | End of the interval, exclusive. |
| `usage.quantity` | string | required | the meter's unit | Measured quantity as fixed-point text with exactly six decimals. Negative for a correction. |
| `usage.unit` | string | required | - | Meter unit, for example second or gibibyte_second. |
| `usage.precision` | string, always `second` | required | - | Source clock precision. Always second; every timestamp is whole seconds. |
| `heartbeat_time` | string | required | UTC, whole seconds | Round time that produced the event. It is part of the deduplication key. |
| `last_applied_offset` | integer | required | event-log offset | Position in the tenant's event topic the round had folded. |
| `rules_revision` | string | required | - | Rule revision that produced the event, for example 0001. |
| `external_billing_account_id` | string | optional | - | Tenant's external billing account id, when the tenant has one. The provider supplies it; OSAC copies it as given. M360 keeps its own mapping. The event omits it otherwise. |

The billable line that `component` names is listed in the Component column of each resource table below.

## Resources and meters

The values below are the ones events carry in `resource_type`: `bare_metal_instance`, `cluster_order`, `compute_instance`, `external_ip`, `nat_gateway`, `volume`.

The default rule revision is revision `0001`, effective from 1970-01-01T00:00:00Z. It is the newest revision the chart ships, and it is immutable: a rule change ships as the next revision. This reference documents the newest shipped revision only; earlier revisions stay in the chart and are immutable.

### Rule example

This is the default revision `0001` that the chart ships, copied from its rule file. The table and key legend below explain the values.

```yaml
# Default meters, revision 0001. FAILED is never billable. Allocation meters
# bill through DELETING until physical deletion. Volume capacity multiplies by
# the provisioned GiB. Every meter also sends deployment, tenant, project,
# catalog item, template, and meter type dimensions.
- resource: ComputeInstance
  meter: compute
  unit: second
  billable_when:
    states: [RUNNING]
  dimensions: [instance_type, image, boot_disk_size_gib]

- resource: Cluster
  meter: control_plane
  unit: second
  billable_when:
    states: [PROGRESSING, READY]
    turns_billable_once: [READY]
  dimensions: [template, version]

- resource: BareMetalInstance
  meter: allocation
  unit: second
  billable_when:
    states: [STARTING, RUNNING, STOPPING, STOPPED, DELETING]
    turns_billable_once: [STARTING, RUNNING]
  dimensions: [instance_type, cluster, node_set]

- resource: BareMetalInstance
  meter: consumption
  unit: second
  billable_when:
    states: [RUNNING]
  dimensions: [instance_type, cluster, node_set]

- resource: Volume
  meter: capacity
  unit: gibibyte_second
  billable_when:
    states: [AVAILABLE, DELETING]
    turns_billable_once: [AVAILABLE]
    fields:
      protocol: BLOCK
      vendor_volume_id: set
  multiplier: provisioned_size_gib
  dimensions: [storage_tier]

- resource: ExternalIP
  meter: allocation
  unit: second
  billable_when:
    states: [ALLOCATED, DELETING]
    turns_billable_once: [ALLOCATED]
  dimensions: [pool, ip_family, attached]

- resource: NATGateway
  meter: allocation
  unit: second
  billable_when:
    states: [READY, DELETING]
    turns_billable_once: [READY]
  dimensions: [virtual_network, external_ip]
```

The rule file names each resource by its proto message; events carry the wire value:

| Rule file `resource` | Event `resource_type` |
|---|---|
| `BareMetalInstance` | `bare_metal_instance` |
| `Cluster` | `cluster_order` |
| `ComputeInstance` | `compute_instance` |
| `ExternalIP` | `external_ip` |
| `NATGateway` | `nat_gateway` |
| `Volume` | `volume` |

| Key | Meaning |
|---|---|
| `resource` | The resource type the rule bills, by proto message name. The table above maps it to the event value. |
| `meter` | The meter name. Events carry it in `meter`. |
| `unit` | What the meter measures. Events carry it in `usage.unit` and `billing_dimensions.meter_type`. |
| `billable_when.states` | The resource states that bill. |
| `turns_billable_once` | States that confirm a deferred billable span. A span that closes before one never bills. |
| `billable_when.fields` | Conditions on fields fixed at creation: the expected text, or `set` for presence. |
| `multiplier` | A tracked field the quantity is multiplied by. Omitted means 1. |
| `dimensions` | The YAML entries are keys in `billing_dimensions`; each resource section lists the tracked field the value comes from. |

A provider that needs different meters ships the next immutable revision with a later effective date; the chart ships revision `0001`. See [the metering chart rules](../../charts/osac-metering/rules/).

`Multiplier` names the tracked field metering has already multiplied into `usage.quantity`, or `none`. A reader must not apply it again. In revision `0001`, these meters use a multiplier: Volume `capacity` by `provisioned_size_gib`. A 100 GiB volume's quantity is already multiplied by 100.

Every usage event also carries these identity dimensions in `billing_dimensions`, besides the meter's own dimensions:

| Dimension | Value |
|---|---|
| `deployment` | Id of the installation that wrote the event |
| `tenant` | Tenant the usage belongs to |
| `meter_type` | The meter's unit |
| `project` | Value of the resource's `metadata.project` |
| `catalog` | The `spec.catalog_item` reference's `name` when set, else its `id` |
| `template` | The `spec.template` reference's `name` when set, else its `id` |

A boolean dimension such as `attached` carries `true` or `false`. A dimension with no value for the interval is omitted from `billing_dimensions`; do not read a missing dimension as an empty string.

### Bare metal instance (`bare_metal_instance`)

Proto message `osac.private.v1.BareMetalInstance`.

| Meter | Component | Unit | Bills while | Turns billable once | Multiplier | Dimensions |
|---|---|---|---|---|---|---|
| `allocation` | `allocation` | `second` | STARTING, RUNNING, STOPPING, STOPPED, DELETING | STARTING, RUNNING | none | `cluster`, `instance_type`, `node_set` |
| `consumption` | `consumption` | `second` | RUNNING | none | none | `cluster`, `instance_type`, `node_set` |

Dimensions:

| Dimension | Value from |
|---|---|
| `cluster` | `status.cluster` |
| `instance_type` | `spec.instance_type` |
| `node_set` | `status.node_set` |

Tracked fields:

| Field | Tracked as | Transition time | Owner |
|---|---|---|---|
| `spec.catalog_item` | constant | none | none |
| `spec.instance_type` | constant | none | none |
| `status.cluster` | fixed_once_set | none | `Cluster` |
| `status.node_set` | fixed_once_set | none | none |
| `status.state` | varies | `status.state_transition_time` | none |

### Cluster order (`cluster_order`)

Proto message `osac.private.v1.Cluster`.

| Meter | Component | Unit | Bills while | Turns billable once | Multiplier | Dimensions |
|---|---|---|---|---|---|---|
| `control_plane` | `control_plane` | `second` | PROGRESSING, READY | READY | none | `template`, `version` |

Dimensions:

| Dimension | Value from |
|---|---|
| `template` | `spec.template` |
| `version` | `spec.version` |

Tracked fields:

| Field | Tracked as | Transition time | Owner |
|---|---|---|---|
| `spec.template` | constant | none | none |
| `spec.version` | varies | `status.version_transition_time` | none |
| `status.state` | varies | `status.state_transition_time` | none |

### Compute instance (`compute_instance`)

Proto message `osac.private.v1.ComputeInstance`.

| Meter | Component | Unit | Bills while | Turns billable once | Multiplier | Dimensions |
|---|---|---|---|---|---|---|
| `compute` | `compute` | `second` | RUNNING | none | none | `boot_disk_size_gib`, `image`, `instance_type` |

Dimensions:

| Dimension | Value from |
|---|---|
| `boot_disk_size_gib` | `spec.boot_disk.size_gib` |
| `image` | `spec.disk_image` |
| `instance_type` | `status.instance_type` |

Tracked fields:

| Field | Tracked as | Transition time | Owner |
|---|---|---|---|
| `spec.boot_disk.size_gib` | constant | none | none |
| `spec.catalog_item` | constant | none | none |
| `spec.disk_image` | constant | none | none |
| `status.instance_type` | varies | `status.instance_type_transition_time` | none |
| `status.state` | varies | `status.state_transition_time` | none |

### External IP (`external_ip`)

Proto message `osac.private.v1.ExternalIP`.

| Meter | Component | Unit | Bills while | Turns billable once | Multiplier | Dimensions |
|---|---|---|---|---|---|---|
| `allocation` | `allocation` | `second` | ALLOCATED, DELETING | ALLOCATED | none | `attached`, `ip_family`, `pool` |

Dimensions:

| Dimension | Value from |
|---|---|
| `attached` | `status.attached` |
| `ip_family` | `status.address`, derived by metering from the tracked value: `ipv4` or `ipv6` |
| `pool` | `spec.pool` |

Tracked fields:

| Field | Tracked as | Transition time | Owner |
|---|---|---|---|
| `spec.pool` | constant | none | none |
| `status.address` | fixed_once_set | none | none |
| `status.attached` | varies | `status.attachment_transition_time` | none |
| `status.attribution` | varies | `status.attachment_transition_time` | none |
| `status.state` | varies | `status.state_transition_time` | none |

### NAT gateway (`nat_gateway`)

Proto message `osac.private.v1.NATGateway`.

| Meter | Component | Unit | Bills while | Turns billable once | Multiplier | Dimensions |
|---|---|---|---|---|---|---|
| `allocation` | `allocation` | `second` | READY, DELETING | READY | none | `external_ip`, `virtual_network` |

Dimensions:

| Dimension | Value from |
|---|---|
| `external_ip` | `spec.external_ip` |
| `virtual_network` | `spec.virtual_network` |

Tracked fields:

| Field | Tracked as | Transition time | Owner |
|---|---|---|---|
| `spec.external_ip` | constant | none | none |
| `spec.virtual_network` | constant | none | none |
| `status.state` | varies | `status.state_transition_time` | none |

### Volume (`volume`)

Proto message `osac.private.v1.Volume`.

| Meter | Component | Unit | Bills while | Turns billable once | Multiplier | Dimensions |
|---|---|---|---|---|---|---|
| `capacity` | `capacity` | `gibibyte_second` | AVAILABLE, DELETING | AVAILABLE | `provisioned_size_gib` | `storage_tier` |

Meter `capacity` also bills only when `protocol` is `BLOCK` and `vendor_volume_id` is `set`.

Dimensions:

| Dimension | Value from |
|---|---|
| `storage_tier` | `spec.storage_tier` |

Tracked fields:

| Field | Tracked as | Transition time | Owner |
|---|---|---|---|
| `spec.storage_tier` | constant | none | none |
| `status.protocol` | constant | none | none |
| `status.provisioned_size_gib` | fixed_once_set | none | none |
| `status.state` | varies | `status.state_transition_time` | none |
| `status.vendor_volume_id` | fixed_once_set | none | none |

## Billing account id

Schema version `v1` and later carry `external_billing_account_id` when the tenant has one. A tenant without one omits it. The field is optional. The provider supplies it and ensures it matches the account in their billing system; OSAC copies it as given. M360 keeps its own mapping.

The id never correlates usage. Match a correction to the event it corrects by tenant, resource, meter, component, and billing dimensions. The id can change while usage is in flight: metering stamps each event with the id current when its round builds it and never rewrites published events.

## Regenerating this reference

Do not edit this file or `schema.json` by hand. Run `make -C osac-metering contract`. CI regenerates both and fails on any difference, so a change to a resource, a meter, or a schema file arrives with its regenerated reference.

Every schema version file is pinned by SHA-256 in `osac-metering/schema/published.sha256`; a new version adds its own entry, and CI fails when a published file differs from it.
