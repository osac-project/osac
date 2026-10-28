# osac-metering

> [!WARNING]
> Be mindful of the content you commit to this repository. Do not commit any
> material containing Red Hat confidential content, including information about
> future product development plans.

Metering pipeline for OSAC — folds fulfillment's per-tenant event topics into
usage, sends per-minute differences to the billing provider, and can rebuild
everything from Kafka.

## How it works

Fulfillment publishes every billable change as an event on each tenant's
`osac.events.<tenant>` topic. Three loops run against one PostgreSQL database:

| Loop | Runs | What it does |
|---|---|---|
| Fold | Continuously | Copies each event's facts into `history`, idempotently |
| Round | Every minute, per tenant | Catches up, computes expected usage from history and rules, and writes the difference with the ledger to the outbox |
| Relay | Continuously | Sends outbox heartbeats to `osac.metering.heartbeat.<tenant>` and advances the heartbeat offset |

Billing rules are data: versioned, immutable ConfigMaps
(`osac-metering-rules-<NNNN>`) holding complete meter definitions and an
`effective_at` time. The chart ships the default meters as revision 0001.

Heartbeats are CloudEvents (`osac.resource.heartbeat.v1`) carrying a usage
interval and a quantity, positive or negative. The billing provider adds them
up; it never replaces one, and it drops heartbeat IDs it has already seen.
That is what makes a crash between a Kafka send and the outbox delete safe: the
same rows are resent with the same IDs.

Everything is rebuildable: history from the event topics, the ledger from the
heartbeat topic, revisions from the ConfigMaps. Losing or restoring the
database never bills usage twice. It also loses nothing as long as the topics
still hold their records: the tenant topics keep every record by default
(`topics.retentionMs: "-1"`). A finite retention bounds the recovery window
to it — records Kafka deleted cannot be replayed after a restore.

## Components

| Directory | Description |
|-----------|-------------|
| `schema/` | Shared heartbeat schema — usage and heartbeat data types, resource type constants |
| `metering-service/` | Fold, heartbeat rounds, relay, rule revisions, and usage evaluation |
| `adapters/` | Provider adapter framework (`Runner`, Kafka consumer, dedup, retry, DLQ routing) and concrete adapters (`echo-adapter`, `m360-adapter`) |
| `charts/osac-metering/` | Helm chart for the metering subsystem, including rule revision 0001 and the policy that protects it |

## Build and Test

```bash
# schema (shared types — no build step, test + lint only)
cd schema
make test     # Run unit tests (ginkgo)
make lint     # Run golangci-lint
cd ..

# metering-service
cd metering-service
make test     # Run unit and integration tests (ginkgo + go test)
make lint     # Run golangci-lint
cd ..

# adapters
cd adapters
make test     # Run unit tests (ginkgo, recursive)
make lint     # Run golangci-lint
```

Integration tests run against real PostgreSQL and Kafka in containers; they
need `podman` or `docker`. The M360 contract test additionally needs
`M360_TEST_API_URL`, `M360_TEST_API_KEY`, and `M360_TEST_TENANT`, and skips
itself without them. Deploying an M360 test tenant is outside this
repository's scope.

## Adding a billable resource

The pipeline is generic: the fold, the rounds, and the relay carry no
per-resource logic. A new resource type is declarations, one migration, and,
when billing through M360, one adapter route.

1. **Annotate the fields.** In the resource's private proto, mark each
   billable field with the metering option. `kind` says how the field behaves:

   | Annotation | Meaning |
   |---|---|
   | `METERING_FIELD_KIND_CONSTANT` | Set at creation, never changes |
   | `METERING_FIELD_KIND_FIXED_ONCE_SET` | Empty, then set once, never changes |
   | `METERING_FIELD_KIND_VARIES` | Changes over time; `transition_time` names the field that carries the change time |

   Run `make -C proto generate`, and update the pinned tables in
   `proto/metering/metering_test.go` together with the annotations.

2. **Add the event payload.** Add a case for the resource to the `Event`
   message's payload oneof in `proto/private/osac/private/v1/event_type.proto`.
   The publisher derives its table-to-payload mapping from this oneof.
3. **Map the table.** Add the proto message to `meteringTables` in
   `fulfillment-service/internal/database/metering.go`.
4. **Attach the triggers.** Write a fulfillment migration that attaches the
   source-contract triggers and the outbox trigger to the resource's table;
   copy the most recent metering migration as the pattern. `CheckSchema` fails
   the service at startup when a mapped table has no triggers, and the drift
   test (`metering_drift_test.go` in the migrations package) fails when the
   triggers disagree with the annotations.
5. **Ship a rules revision.** Add a ConfigMap under
   `charts/osac-metering/rules/` naming the resource's meters: unit, billable
   states, dimensions. A revision is immutable and needs a future
   `effective_at`.
6. **Route the provider.** Billing through M360 needs the resource type in
   the M360 adapter's route table. The echo adapter is generic.

## Deployment

The metering subsystem is deployed by the osac-installer umbrella chart,
always: billing is not optional, and no value turns it off. Prerequisites:
AMQ Streams operator and Kafka cluster (installed by osac-installer phases 1
and 2 with `kafka.enabled: true`), and Kubernetes 1.30 or later for the
admission policy that protects rule revisions.

Kafka ACLs: the event publisher creates and writes the event topics; metering
reads them, creates the heartbeat and dead-letter topics of the tenants it
serves, and reads and writes heartbeat topics; the adapters read heartbeat
topics and write dead-letter topics.

## Replaying dead-lettered heartbeats

Rejected heartbeats land in the tenant's `osac.metering.dlq.<tenant>` topic.
Once the cause is fixed, replay them:

1. Stop the adapter.
2. Reset its position on the tenant's heartbeat topic to the earliest rejected
   record's original offset — the `original-offset` header of the DLQ record —
   for example with `kafka-consumer-groups --reset-offsets --group
   osac-metering-m360-adapter`.
3. Restart the adapter. Re-reading the heartbeat topic re-submits everything
   from that offset on; the billing provider drops the IDs it already has, so
   only the previously rejected heartbeats bill.

The DLQ itself has no consumer: no group tracks which records have been
handled, so there is no mechanism to mark the DLQ handled, and nothing deletes
its records on replay. The `osac_metering_dlq_depth` gauge reports retained
records (log occupancy, newest minus oldest offsets), not unhandled ones, and
it shrinks only as the topic's retention deletes records. A handled position
for the DLQ — the consumer group the design calls for, with the notification
keyed to it — does not exist yet.
