# osac-metering

Metering pipeline for OSAC — collects resource usage events from the fulfillment-service, publishes CloudEvents to Kafka, and provides a Provider Adapters framework for downstream billing integrations.

## Critical Rules

- **Three Go modules** — `schema/` (shared types), `metering-service/` (producer), and `adapters/` (consumer framework) have independent `go.mod`; both `metering-service` and `adapters` depend on `schema` via `replace` directives
- **Always `make test`** in the module you changed before committing
- **Always `make helm-lint`** from `osac-metering/` before committing chart changes
- **Kafka cluster required** — metering subsystem needs AMQ Streams and Kafka (deployed via osac-installer phases 1-2)
- **CloudEvents format** — all metering events use CloudEvents specification for consistency
- **Implement `ProviderAdapter`** — new billing integrations implement the `ProviderAdapter` interface in `adapters/adapter.go`; the `Runner` handles Kafka consumption, dedup, retry, and offset management

## Dev Environment

**Language**: Go | **Framework**: gRPC client + CloudEvents SDK + Sarama | **Build tool**: Make | **Message broker**: Kafka | **Test framework**: Ginkgo v2 + Gomega | **Linter**: golangci-lint

```bash
# Root-level targets (run from osac-metering/)
make helm-lint                 # Lint the Helm chart
make test                      # Run unit tests in both modules
make lint                      # Helm lint + golangci-lint in both modules

# metering-service (producer side)
cd metering-service
make build                     # Build the metering-service binary
make test                      # Run unit tests with Ginkgo
make lint                      # Run golangci-lint
make generate                  # Regenerate proto client code from BSR
make clean                     # Clean build artifacts

# adapters (consumer framework)
cd adapters
make build-echo-adapter        # Build the echo-adapter binary
make build-m360-adapter        # Build the m360-adapter binary
make test                      # Run unit tests with Ginkgo
make lint                      # Run golangci-lint
make clean                     # Clean build artifacts
```

## Component Layout

| Directory | Purpose |
|-----------|---------|
| `schema/` | Shared event schema — resource type constants, CloudEvent extension names, `LifecycleData` struct |
| `metering-service/` | Producer — watches fulfillment-service gRPC Watch stream, publishes CloudEvents to Kafka |
| `adapters/` | Consumer framework — Kafka-to-provider bridge with `ProviderAdapter` interface and `Runner` |
| `charts/osac-metering/` | Helm chart for deploying metering-service, echo-adapter, and m360-adapter |

## Architecture

```text
fulfillment-service (gRPC Watch stream)
  ↓ resource lifecycle events
metering-service (event mapping, heartbeats, reconciliation)
  ↓ CloudEvents
Kafka Topics
  ↓ Sarama consumer group
adapters.Runner (dedup → out-of-order check → retry → Submit → Flush → offset commit)
  ↓ ProviderAdapter interface
Concrete Adapters (echo-adapter, m360-adapter, billing providers)
```

### Key Packages

| Package | Purpose |
|---------|---------|
| `metering-service/internal/watch/` | gRPC client for fulfillment-service Watch stream |
| `metering-service/internal/events/` | Resource event mappers and state transition tables |
| `metering-service/internal/heartbeat/` | Periodic heartbeat event generation |
| `metering-service/internal/reconciliation/` | Correction event generation for drift detection |
| `metering-service/internal/kafka/` | Kafka producer client with delivery guarantees |
| `adapters/` | Provider Adapter framework (see below) |

### Provider Adapters Framework

The `adapters/` package is a standalone Go module that provides everything a billing adapter needs to consume metering events from Kafka:

**`ProviderAdapter` interface** (`adapter.go`) — concrete adapters implement five methods:
- `Name()` — provider label for Prometheus metrics
- `Submit(ctx, event)` — process a single `MeteringEvent` (CloudEvent + Kafka coordinates)
- `Flush(ctx)` — upload buffered events (called on configurable interval and shutdown)
- `HealthCheck(ctx)` — verify provider connectivity
- `Close()` — release resources after final flush

**`Runner`** (`runner.go`) — manages the full Kafka consumer lifecycle:
- Sarama consumer group with manual offset commits (offsets committed only after successful `Flush`)
- TTL-based dedup cache suppresses duplicate CloudEvent IDs
- Out-of-order detection tracks per-resource `transition_time` ordering
- Exponential backoff retry (1s→5m, ±25% jitter, configurable max attempts)
- `NonRetryableError` / `RetryableError` error classification
- Graceful shutdown: final flush with 30s timeout, adapter close with 10s timeout

**Kafka configuration** (`kafka.go`, `kafka_env.go`) — TLS with custom CA, SASL/SCRAM-SHA-512 authentication; `KafkaConfigFromEnv()` reads `KAFKA_*` env vars with TLS enabled by default

**Prometheus metrics** (`metrics.go`):
- `osac_adapter_events_submitted_total` (provider, topic)
- `osac_adapter_events_failed_total` (provider, error_type)
- `osac_adapter_duplicates_suppressed_total` (provider)
- `osac_adapter_out_of_order_events_total` (provider)
- `osac_adapter_flush_duration_seconds` (provider)
- `osac_adapter_flush_errors_total` (provider)
- `osac_adapter_retry_duration_seconds` (provider)

### Echo Adapter

`adapters/cmd/echo-adapter/` is a reference implementation that exercises the full `Runner` lifecycle without connecting to a real billing provider. It logs events to stdout, stores them in a bounded ring buffer, and exposes HTTP query endpoints for E2E test assertions:

- `GET /events` — list stored events
- `GET /events/{id}` — get event by CloudEvent ID
- `GET /events/count` — event count
- `DELETE /events` — clear stored events
- `GET /healthz` — health check
- `GET /metrics` — Prometheus metrics

Kafka TLS is enabled by default (via `KafkaConfigFromEnv()`); set `KAFKA_TLS_ENABLED=false` for local development without TLS.

Deployed via Helm with `echoAdapter.enabled: true` (disabled by default; enabled in CI values profiles).

### M360 Adapter

`adapters/cmd/m360-adapter/` forwards OSAC metering CloudEvents to the Monetize360 (M360) Usage API via REST or Kafka. It translates nested CloudEvents to M360's flat payload format and routes by resource type via protocol-neutral route keys (`vmaas`, `caas`, `maas`).

**Output protocol** is configured per-deployment via `M360_OUTPUT_PROTOCOL` (default `rest`):

- **REST** — POST to M360 Usage API endpoints (`/vmaas/event`, `/caas/event`, `/maas/event`), Bearer token auth from K8s Secret
- **Kafka** — Produce flat M360 JSON to M360 Kafka topics (`m360.metering.vmaas`, etc.), resource_id as message key, idempotent SyncProducer

Common features:
- Per-event submit, error classification: 4xx → non-retryable (except 408/429), 5xx → retryable
- `GET /healthz` — liveness probe (always 200)
- `GET /readyz` — readiness probe (REST: TCP/TLS to M360 base URL; Kafka: metadata refresh)
- `GET /metrics` — Prometheus metrics
- `submitter` interface abstracts output protocol (`restSubmitter`, `kafkaSubmitter`)

Deployed via Helm with `m360Adapter.enabled: true` (disabled by default). Set `m360Adapter.output.protocol: kafka` and configure `m360Adapter.output.kafka.*` for Kafka output.

## Deployment

The metering subsystem is deployed via the osac-installer umbrella chart with `metering.enabled: true`.

**Prerequisites:**
- AMQ Streams operator (installed by osac-installer phase 1)
- Kafka cluster (installed by osac-installer phase 2 with `kafka.enabled: true`)
- fulfillment-service running with gRPC Watch endpoints enabled

**Deployment Order:**
1. osac-installer phases 1-2 (AMQ Streams + Kafka)
2. fulfillment-service (API server)
3. osac-metering (usage collection) — depends on both

## Configuration

Key configuration parameters in `charts/osac-metering/values.yaml`:

- `database.connection` — PostgreSQL connection for state projection
- `heartbeat.interval` — Heartbeat event generation interval (default `60s`)
- `reconciliation.interval` — Reconciliation sweep interval (default `60m`)
- `certs.caBundle.configMap` — CA bundle ConfigMap name
- `echoAdapter.enabled` — Deploy the echo-adapter (default `false`)
- `m360Adapter.enabled` — Deploy the m360-adapter (default `false`)
- `m360Adapter.m360.apiUrl` — M360 Usage API base URL (required when protocol=rest)
- `m360Adapter.m360.apiKeySecret` — K8s Secret name containing the M360 API key (required when protocol=rest)
- `m360Adapter.m360.apiVersion` — M360 API version (default `v1`)
- `m360Adapter.output.protocol` — Output protocol: `rest` or `kafka` (default `rest`)
- `m360Adapter.output.kafka.brokers` — M360 Kafka bootstrap servers (required when protocol=kafka)
- `m360Adapter.output.kafka.tls.*` — TLS config for M360 Kafka (enabled by default)
- `m360Adapter.output.kafka.sasl.*` — SASL/SCRAM config for M360 Kafka
- `m360Adapter.output.kafka.topics.*` — Topic name overrides (defaults: `m360.metering.vmaas/caas/maas`)

## CI Integration

- **build-metering-service-image.yaml** — Builds and pushes metering-service container image
- **build-metering-echo-adapter-image.yaml** — Builds echo-adapter image on `osac-metering/adapters/**` changes
- **build-metering-m360-adapter-image.yaml** — Builds m360-adapter image on `osac-metering/adapters/**` changes
- **run-osac-metering-tests** — Runs unit tests as part of the mono-repo test suite
- **E2E workflows** (CaaS, VMaaS, BMaaS) — Tests metering in full OSAC deployments

## Testing

- **metering-service unit tests** — `make test` in `metering-service/`
- **adapters unit tests** — `make test` in `adapters/` (covers Runner, dedup, retry, order tracker, metrics, echo-adapter, m360-adapter)
- **E2E validation** — echo-adapter deployed in CI, queried via HTTP to assert event delivery
- **Integration tests** — Included in E2E workflows with full Kafka setup
