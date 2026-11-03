# osac-metering

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence. Follow this guide for suite setup and limits; inspect test source for
individual cases. Commands and inline paths are relative to `osac-metering/`
unless explicitly marked as repository-root commands.

Touched-area requirements: [component guide](../../osac-metering/AGENTS.md#integration-testing).

## Test tiers and commands

| Tier | Location / command | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | From the repository root: `make -C osac-metering/schema test`; from `osac-metering/metering-service/`: `go test ./internal/usage ./internal/rules`. Adapter unit specs live in `adapters/`; no unit-only aggregate target is provided. | Schema and rule parsing, pure usage evaluation, and adapter behavior | These unit commands do not start PostgreSQL or Kafka. The adapter Makefile target is listed under Contract because it also runs the real-tenant M360 contract test when configured. |
| Component integration (database) | `metering-service/internal/database`; included by `make test` | A real PostgreSQL testcontainer, the event-log schema, idempotent fact writes, ledger and outbox round trips | Kafka is not exercised. |
| Component integration (Kafka and database) | `metering-service/internal/fold` and `metering-service/internal/round`; included by `make test` | A real Kafka broker and PostgreSQL: fold determinism across restarts, duplicates, and shuffles; heartbeat rounds, catch-up and recovery, relay resend with the same IDs | The billing provider and fulfillment are not exercised; events are produced directly. |
| Component integration (combined pipeline) | `metering-service/internal/pipeline`; included by `make test` | A real Kafka broker and PostgreSQL with the fold, round, and relay wired in one process: envelope, per-interval continuation, stop, resize split, delete, and volume billing, asserted on the tenant's heartbeat topic | The billing provider and fulfillment are not exercised; events are produced directly and rounds run at fixed times. |
| Contract | From repository root, set `M360_TEST_API_URL`, `M360_TEST_API_KEY`, and `M360_TEST_TENANT`, then run `make -C osac-metering/adapters test` | A real M360 tenant: submits positive and negative quantities, duplicate IDs, dated heartbeats, invalid heartbeats, and a heartbeat dated 48 hours earlier. It asserts synchronous acceptance or rejection; the target also runs adapter unit suites. | The contract test skips unless all three real-tenant variables are set. It does not read billed totals or verify that the 48-hour-old heartbeat falls in an invoiced period; correction behavior is unverified. The adapter has no usage-query path, and no specific owner or follow-up ticket has been identified for these gaps. The related [OSAC-4286](https://redhat.atlassian.net/browse/OSAC-4286) provider E2E epic is unassigned and does not assign this readback work. Deploying an M360 tenant is outside this repository's scope. |
| E2E | Cross-component OSAC metering/E2E deployment | The deployed metering pipeline and its configured Kafka/provider dependencies | Depends on the installer environment and enabled metering path. |
| Helm render and lint | `make -C osac-metering helm-lint` (repository root) | Chart templates, rule revision 0001, and the admission policy that protects it | Deployed behavior. |
| Helm render | `make -C osac-installer metering-auth-render-test` (repository root) | Metering Deployment, rules ConfigMap, admission policy and binding, and absence of fulfillment credentials in the vmaas-ci, bmaas-ci, caas-ci, and full-ci profiles | Does not verify a deployed metering pipeline. |

From the repository root, `make -C osac-metering test` runs the full component
suite across schema, metering-service, adapters, and rule revision checks. It
includes the PostgreSQL and Kafka integration suites below; do not describe it
as a unit-only command.

## Coverage notes

- **Event schema or usage evaluation:** Schema changes affect `schema/`, `metering-service/`, and `adapters/`; run the root `make test` after schema changes.
- **Fold, database, rounds, or relay:** Do not skip the real PostgreSQL and Kafka suites; they are the determinism gate for billing.
- **Rule revisions:** `metering-service/internal/rules` checks that shipped revisions parse, use only tracked fields, never change, and that the chart's meters match the ones in code.
- **Provider adapters:** The shared runner must remain the owner of ordering, retry, deduplication, and DLQ behavior; the DLQ is mandatory.

## Coverage gaps

- Deployed cross-component journeys (tenant onboarding creating the three topics) belong to the `[QE]` E2E work; the event-to-heartbeat path on a hub is covered by the metering sanity tier in `../tests/e2e/vmaas/sanity/test_metering_billing.py`.
- The M360 contract test needs a real tenant configured per environment; it is skipped when the variables are absent. It verifies submission and synchronous acceptance or rejection. Verifying billed totals needs a usage query path the adapter does not have; this remains unresolved, with no specific owner or follow-up ticket identified. The related [OSAC-4286](https://redhat.atlassian.net/browse/OSAC-4286) provider E2E epic is unassigned and does not assign this readback work.
