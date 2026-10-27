# OSAC metering

Folds fulfillment's per-tenant event topics into usage, sends per-minute
differences to the billing provider, and provides provider adapters for
downstream billing integrations.

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
- Fold, rounds, relay, and rules: `metering-service/`
- Adapter runner and contracts: `adapters/`
- Deployment values and rule revisions: `charts/osac-metering/`

## Invariants

- `schema/`, `metering-service/`, and `adapters/` are separate Go modules; test the module you change.
- Changes under `schema/` affect both `metering-service/` and `adapters/`; run the root `make test` after schema changes.
- Metering events use the shared CloudEvents schema and preserve resource transition ordering.
- The same events and rules always produce the same usage, across restarts, upgrades, and database restores.
- The fold writes facts and event offsets in one transaction; nothing bills twice and none is lost.
- Kafka offsets are committed only after successful processing and flush.
- Preserve deduplication, ordering, retry, and DLQ behavior in the shared adapter `Runner`; concrete adapters must not reimplement it.
- A DLQ send failure must not silently acknowledge the source event.
- New billing integrations implement `ProviderAdapter` and use the shared runner lifecycle.
- Keep Kafka credentials and API keys out of logs, fixtures, examples, and manifests.

## Integration Testing

See [suite boundaries and coverage gaps](../docs/INTEGRATION-TESTING.md#osac-metering).

| Touched area | Required validation | Command / follow-up |
|---|---|---|
| Event schema or heartbeat shape | Unit across affected modules | `make test` |
| Database, fold, rounds, relay, or usage evaluation | Component integration against real PostgreSQL and Kafka | `make test` |
| Rule revisions | Unit, plus the chart-sync check against the shipped ConfigMap | `make test` |
| Kafka producer/consumer, CloudEvents transport, offsets, retries, or DLQ | Component integration | `make test` |
| Provider adapters | Unit plus component/E2E coverage for the provider boundary | `make test` and the qualifying provider suite |
| Billing provider contract | Contract test against a real M360 tenant | `M360_TEST_API_URL`/`M360_TEST_API_KEY`/`M360_TEST_TENANT` gate `adapters/m360/contract_test.go` |

Do not skip the real PostgreSQL and Kafka suites when validating database,
fold, round, or relay behavior; they are the determinism gate for billing.

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
