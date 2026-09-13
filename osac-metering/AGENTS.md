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

## Integration Testing

See [suite boundaries and coverage gaps](../docs/INTEGRATION-TESTING.md#osac-metering).

| Touched area | Required validation | Command / follow-up |
|---|---|---|
| Event schema or transition mapping | Unit across affected modules | `make test` |
| Projection/database code | Component integration (database) | `make test` |
| Kafka producer/consumer, CloudEvents transport, offsets, retries, or DLQ | Component integration | Required suite is currently unavailable; track [OSAC-4846](https://redhat.atlassian.net/browse/OSAC-4846) |
| Fulfillment Watch or gRPC event ingestion | Contract or component integration | Required suite is currently unavailable; track the relevant [OSAC-4843](https://redhat.atlassian.net/browse/OSAC-4843) task |
| Provider adapters | Unit plus component/E2E coverage for the provider boundary | `make test` and the qualifying provider suite |

Do not set `SKIP_DB_TESTS` when validating projection/database behavior.

## Generated files

- Fulfillment proto types are the shared top-level `proto/` module, imported as `github.com/osac-project/osac/proto/gen/...`. `metering-service/` no longer generates its own copy; `make generate` there just delegates to `make -C ../../proto generate`. Commit the regenerated `proto/gen/` (see `proto/AGENTS.md`); never edit generated code manually.
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
