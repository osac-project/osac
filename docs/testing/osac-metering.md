# osac-metering

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence. Follow this guide for suite setup and limits; inspect test source for
individual cases. Commands and inline paths are relative to `osac-metering/`
unless explicitly marked as repository-root commands.

Touched-area requirements: [component guide](../../osac-metering/AGENTS.md#integration-testing).

## Test tiers and commands

| Tier | Location / command | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | Co-located tests in `schema/`, `metering-service/` (rules, usage evaluation), and `adapters/`; `make test` | Schema, rule revision parsing and startup checks, usage evaluation (meters, gate semantics, duration arithmetic), adapter translation, runner, retry, ordering | Kafka and PostgreSQL are not required. |
| Component integration (database) | `metering-service/internal/database`; included by `make test` | A real PostgreSQL testcontainer, the event-log schema, idempotent fact writes, ledger and outbox round trips | Kafka is not exercised. |
| Component integration (Kafka and database) | `metering-service/internal/fold` and `metering-service/internal/round`; included by `make test` | A real Kafka broker and PostgreSQL: fold determinism across restarts, duplicates, and shuffles; heartbeat rounds, catch-up and recovery, relay resend with the same IDs | The billing provider and fulfillment are not exercised; events are produced directly. |
| Component integration (combined pipeline) | `metering-service/internal/pipeline`; included by `make test` | A real Kafka broker and PostgreSQL with the fold, round, and relay wired in one process: envelope, per-interval continuation, stop, resize split, delete, and volume billing, asserted on the tenant's heartbeat topic | The billing provider and fulfillment are not exercised; events are produced directly and rounds run at fixed times. |
| Contract | `adapters/m360/contract_test.go`; runs when `M360_TEST_API_URL`, `M360_TEST_API_KEY`, and `M360_TEST_TENANT` are set, skips otherwise | A real M360 tenant: submits positive and negative quantities, duplicate IDs, dated heartbeats, invalid heartbeats, and invoiced-period corrections, and asserts synchronous acceptance or rejection | Reading the tenant's usage totals back is a gap: the adapter has no query path, so rate and deduplication arithmetic is not verified end to end. Deploying the M360 tenant is out of scope for this repository. |
| E2E | Cross-component OSAC metering/E2E deployment | The deployed metering pipeline and its configured Kafka/provider dependencies | Depends on the installer environment and enabled metering path. |
| Helm render and lint | `make -C osac-metering helm-lint` (repository root) | Chart templates, rule revision 0001, and the admission policy that protects it | Deployed behavior. |
| Helm render | `make -C osac-installer metering-auth-render-test` (repository root) | Metering Deployment, rules ConfigMap, admission policy and binding, and absence of fulfillment credentials in the vmaas-ci, bmaas-ci, caas-ci, and full-ci profiles | Does not verify a deployed metering pipeline. |

## Coverage notes

- **Event schema or usage evaluation:** Schema changes affect `schema/`, `metering-service/`, and `adapters/`; run the root `make test` after schema changes.
- **Fold, database, rounds, or relay:** Do not skip the real PostgreSQL and Kafka suites; they are the determinism gate for billing.
- **Rule revisions:** `metering-service/internal/rules` checks that shipped revisions parse, use only tracked fields, never change, and that the chart's meters match the ones in code.
- **Provider adapters:** The shared runner must remain the owner of ordering, retry, deduplication, and DLQ behavior; the DLQ is mandatory.

## Coverage gaps

- Deployed cross-component journeys (tenant onboarding creating the three topics) belong to the `[QE]` E2E work; the event-to-heartbeat path on a hub is covered by the metering sanity tier in `../tests/e2e/vmaas/sanity/test_metering_billing.py`.
- The M360 contract test needs a real tenant configured per environment; it is skipped when the variables are absent. It verifies submission and synchronous acceptance or rejection; verifying the billed totals needs a usage query path the adapter does not have yet.
