# osac-metering

> [!WARNING]
> Be mindful of the content you commit to this repository. Do not commit any
> material containing Red Hat confidential content, including information about
> future product development plans.

Metering pipeline for OSAC — collects resource usage events and publishes them to Kafka for downstream billing adapters.

## Components

| Directory | Description |
|-----------|-------------|
| `schema/` | Shared metering event schema — lifecycle data types, resource type constants, CloudEvent extension names |
| `metering-service/` | Watch Consumer: connects to fulfillment-service gRPC Watch stream, maps lifecycle events to CloudEvents, publishes to Kafka |
| `adapters/` | Provider Adapter framework (`Runner`, Kafka consumer, dedup, retry, DLQ routing) and concrete adapters (`echo-adapter`, `m360-adapter`) |
| `charts/osac-metering/` | Helm umbrella chart for the metering subsystem |

## Build and Test

```bash
# schema (shared types — no build step, test + lint only)
cd schema
make test     # Run unit tests (ginkgo)
make lint     # Run golangci-lint
cd ..

# metering-service (producer)
cd metering-service
make build    # Build the metering-service binary
make test     # Run unit tests (ginkgo)
make lint     # Run golangci-lint
make generate # Regenerate proto client code from BSR
cd ..

# adapters (consumer framework)
cd adapters
make test     # Run unit tests (ginkgo, recursive)
make lint     # Run golangci-lint
```

## Deployment

The metering subsystem is deployed via the osac-installer umbrella chart with `metering.enabled: true`. Prerequisites: AMQ Streams operator and Kafka cluster (installed by osac-installer phases 1 and 2 with `kafka.enabled: true`).
