# OSAC metering

Never edit this file except for minor corrections. Do not append new content.

Folds fulfillment's per-tenant event topics into usage, sends per-minute
differences to the billing provider, and provides provider adapters for
downstream billing integrations.

## Required context

Read the applicable references before changing behavior:

- Component setup: [`README.md`](README.md)
- Event schema: `schema/`
- Published contract: `docs/contract/`; source inputs are the field registry,
  `schema/versions/`, chart defaults in `charts/osac-metering/values.yaml`,
  and the default rule revision under `charts/osac-metering/rules/` and
  `charts/osac-metering/templates/rules.yaml`.
- Fold, rounds, relay, and rules: `metering-service/`
- Adapter runner and contracts: `adapters/`
- Deployment values and rule revisions: `charts/osac-metering/`

## Invariants

- `schema/`, `metering-service/`, and `adapters/` are separate Go modules; test the module you change.
- Changes under `schema/` affect both `metering-service/` and `adapters/`; run `make test` from `osac-metering/` after schema changes.
- Metering events use the shared CloudEvents schema and preserve resource transition ordering.
- The same events and rules always produce the same usage, across restarts, upgrades, and database restores.
- Released rule revisions are immutable; add a new revision instead.
  `make rules-unchanged-test` verifies the release-tag boundary with full history.
- The fold writes facts and event offsets in one transaction; nothing bills twice and none is lost.
- Kafka offsets are committed only after successful processing and flush.
- Preserve deduplication, ordering, retry, and DLQ behavior in the shared adapter `Runner`; concrete adapters must not reimplement it.
- A DLQ send failure must not silently acknowledge the source event.
- New billing integrations implement `ProviderAdapter` and use the shared runner lifecycle.
- Keep Kafka credentials and API keys out of logs, fixtures, examples, and manifests.

## Integration Testing

All commands below run from `osac-metering/` unless a module cwd is stated.

See [suite boundaries and coverage gaps](../docs/INTEGRATION-TESTING.md#osac-metering).

| Touched area | Required validation | Command / follow-up |
|---|---|---|
| Event schema or heartbeat shape | Unit across affected modules | `make test` |
| Database, fold, rounds, relay, or usage evaluation | Component integration against real PostgreSQL and Kafka | `make test` |
| Rule revisions | Unit, chart-sync and released-rule immutability | `make test` (includes `rules-render-test rules-unchanged-test`) |
| Generated contract or published schema files | Unit plus the contract check and the release gate | `make contract-check schemas-unchanged-test` (the CI leg runs with full git history so the release-tag comparison can run) |
| Kafka producer/consumer, CloudEvents transport, offsets, retries, or DLQ | Component integration | `make test` |
| Provider adapters | Unit plus component/E2E coverage for the provider boundary | `make test` and the qualifying provider suite |
| Reader grants and ACLs | Chart render unit | `make helm-test readers-render-test` (requires helm-unittest) |
| Worked example reader (`adapters/examples/billing-reader`) | Unit; component integration against a real Kafka topic is a gap | `make -C adapters test` |
| Billing provider contract | Contract test against a real M360 tenant | `M360_TEST_API_URL`/`M360_TEST_API_KEY`/`M360_TEST_TENANT` gate `adapters/m360/contract_test.go` |

Do not skip the real PostgreSQL and Kafka suites when validating database,
fold, round, or relay behavior; they are the determinism gate for billing.

## Generated files

- Fulfillment types are generated in shared `../proto/`; from `osac-metering/`,
  run `make -C ../proto generate` and commit its generated trees. Never hand-edit them.
- Change contract sources, then run `make contract` from `osac-metering/`.
  Never hand-edit the generated reference or schema under `docs/contract/`.
- A published version file is pinned in `schema/published.sha256`. A new version file needs its own entry; CI fails when an existing file's bytes differ from the manifest.
- Use `go mod tidy` for dependency updates in the affected module.

## Common Tasks

### Change event ingestion or usage calculation

- Follow the event through `metering-service/internal/kafka/`, `fold/`,
  `round/` and `relay/` rather than treating collection and billing as one
  operation. Check event offsets, persisted facts and replay behavior together.
- Use neighboring tests and the `testenv`, `kafkatest` and `pipelinetest`
  helpers under `metering-service/internal/` for the relevant boundary. Keep
  restart/replay and duplicate-event cases tied to the real PostgreSQL/Kafka
  harness when persistence or delivery semantics change.
- For event-shape changes, update `schema/` and validate both service and
  adapter consumers with `make test` from `osac-metering/`.

### Add a rule revision or update the published contract

- Add a new revision under `charts/osac-metering/rules/`; never modify a
  released revision in place. Keep the selected default and rendered rule
  configuration consistent with the usage evaluator.
- Change the contract's registry/schema/chart sources, not `docs/contract/`.
  From `osac-metering/`, run `make contract`, review the generated reference,
  then run `make contract-check schemas-unchanged-test` and `make test`.
  Preserve full release history for the immutability comparison.

### Add a billing adapter

- Implement the `ProviderAdapter` interface defined in `adapters/adapter.go`
  in a concrete package following `adapters/echo/` or `adapters/m360/`.
  Use the shared Runner for delivery, retry and shutdown rather than starting
  an independent consumer.
- Test provider mapping and Submit/Flush error handling locally. Error text
  can reach DLQ headers, so it must not contain credentials or raw secrets.
- Run `make -C adapters test` from `osac-metering/` for adapter work, plus the
  relevant provider contract when its authorized prerequisites are available.
  A fake provider does not prove external acceptance or billing behavior.

## Validation

### Component checks

Run from `osac-metering/`, with Go, Helm and the helm-unittest plugin available.
Integration suites require real PostgreSQL/Kafka and their test harness
prerequisites; retain full Git history/tags for released-rule/schema checks.

```bash
make test                  # schema, metering-service, and adapters
make lint
make helm-lint
make helm-test             # chart render tests; needs the helm-unittest plugin
make readers-render-test
make contract-check schemas-unchanged-test
```

`make test` includes module tests, rule rendering/immutability, schema
immutability and reader rendering. `make contract-check` additionally checks
generated contract freshness and service dependency boundaries.

### Focused module work and builds

- For isolated changes, run `make test` and `make lint` in the changed module
  and every directly affected module; schema changes require the component gate.
- Build the service with `make build` from `metering-service/`.
- Build adapters with `make build-echo-adapter` or `make build-m360-adapter`
  from `adapters/`.
- Regenerate contract docs with `make contract` from `osac-metering/`; use
  `make -C ../proto generate` there for shared protobuf changes, not a
  module-relative path copied into the wrong cwd.
- Deployed Kafka/E2E checks require installer infrastructure. Real billing
  provider contract tests additionally require their explicitly configured
  tenant and credentials; never substitute mock-provider success.
