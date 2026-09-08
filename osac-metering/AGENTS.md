# OSAC metering

Collects fulfillment lifecycle events, publishes CloudEvents to Kafka, and
provides provider adapters for downstream billing integrations.

This component is part of the OSAC monorepo, not an isolated project. Its APIs,
generated artifacts, deployment configuration, and runtime behavior may affect
other components. Apply the repository-wide rules in
[`../AGENTS.md`](../AGENTS.md), consider downstream consumers before changing
behavior, and follow the instructions for every affected component.

## Required context

Before changing this component, identify the documents relevant to the change
below, then read and follow them. These documents are authoritative for their
respective areas.

- Component setup: [`README.md`](README.md)
- Event schema: `schema/`
- Producer: `metering-service/`
- Adapter runner and contracts: `adapters/`
- Deployment values: `charts/osac-metering/values.yaml`

## Invariants

- `schema/`, `metering-service/`, and `adapters/` are separate Go modules; test the module you change.
- Changes under `schema/` affect both `metering-service/` and `adapters/`; run the root `make test` after schema changes.
- Metering events use the shared CloudEvents schema and preserve resource transition ordering.
- Kafka offsets are committed only after successful processing and flush.
- Preserve deduplication, ordering, retry, and DLQ behavior in the shared adapter `Runner`; concrete adapters must not reimplement it.
- A DLQ send failure must not silently acknowledge the source event.
- New billing integrations implement `ProviderAdapter` and use the shared runner lifecycle.
- Keep Kafka credentials and API keys out of logs, fixtures, examples, and manifests.

## Generated files

- After changing private fulfillment protos consumed by `metering-service`, run `make generate` from `metering-service/` and commit the resulting `internal/api/` changes; never edit generated client code manually.
- Use `go mod tidy` for dependency updates in the affected module.

## Validation

From `osac-metering/`:

```bash
make test                  # schema, metering-service, and adapters
make lint
make helm-lint
```

For isolated changes, run `make test` and `make lint` in the changed module
and any directly affected module. Build adapter binaries with
`make build-echo-adapter` or `make build-m360-adapter` from `adapters/`.
Kafka-backed integration and E2E
validation requires the installer deployment and its Kafka prerequisites.
