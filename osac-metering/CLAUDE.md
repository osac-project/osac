# osac-metering

@AGENTS.md

## Overview

Metering pipeline for OSAC — collects resource usage events from the fulfillment-service, publishes CloudEvents to Kafka, and provides a Provider Adapters framework for downstream billing integrations.

## Build Instructions

Two independent Go modules — run commands from the appropriate subdirectory:

```bash
# metering-service (producer)
cd metering-service
make build                     # Build the metering-service binary
make test                      # Run unit tests
make lint                      # Run golangci-lint

# adapters (consumer framework)
cd adapters
make build-echo-adapter        # Build the echo-adapter binary
make build-m360-adapter        # Build the m360-adapter binary
make test                      # Run unit tests
make lint                      # Run golangci-lint
```

## Integration

- **Deployment**: Via osac-installer with `metering.enabled: true`
- **Prerequisites**: AMQ Streams operator, Kafka cluster, and fulfillment-service with gRPC Watch endpoints enabled
- **Event Flow**: fulfillment-service gRPC Watch → CloudEvents → Kafka → Provider Adapters (via `adapters.Runner`)
- **New adapters**: Implement `ProviderAdapter` interface in `adapters/adapter.go`
