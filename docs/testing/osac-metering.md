# osac-metering

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence. Inspect test source for individual cases. Paths are relative to
`osac-metering/` unless marked as repository-root paths.

Touched-area requirements: [component guide](../../osac-metering/AGENTS.md#integration-testing).

## Test tiers

| Tier | Suite (by directory/role) | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | Go suites in `schema/`, `metering-service/`, and `adapters/` | Schema and rule parsing, usage evaluation, and adapter behavior | No PostgreSQL or Kafka; the adapter aggregate target also runs the real-tenant Contract tier when configured. |
| Generated contract and published schemas | `make contract-check` and `make schemas-unchanged-test` | Regenerates the reference and checkable schema file and compares them with the committed copies; validates every published version and effective date; fails when a file a release tag ships changed, was renamed, or was deleted | The release-tag comparison needs git tags: the CI contract leg checks out with full history, and a checkout without tags passes that gate instead of failing it |
| Component integration (database) | `metering-service/internal/database` | Real PostgreSQL testcontainer: event-log schema, idempotent fact writes, ledger and outbox round trips | Kafka is not exercised. |
| Component integration (Kafka and database) | `metering-service/internal/fold` and `internal/round` | Real Kafka and PostgreSQL: fold determinism across restarts, duplicates, and shuffles; heartbeat rounds, catch-up and recovery; relay resend with the same IDs | Billing provider and fulfillment are not exercised; events are produced directly. |
| Component integration (combined pipeline) | `metering-service/internal/pipeline` | Real Kafka and PostgreSQL with fold, round, and relay in one process: envelope, continuation, stop, resize split, delete, and volume billing on the tenant's heartbeat topic | Billing provider and fulfillment are not exercised; events are produced directly and rounds run at fixed times. |
| Contract | The adapter contract suite, gated on the `M360_TEST_API_URL`, `M360_TEST_API_KEY`, and `M360_TEST_TENANT` variables | A real M360 tenant: positive and negative quantities, duplicate IDs, dated and invalid heartbeats, and a heartbeat dated 48 hours earlier, with synchronous acceptance or rejection asserted | Skips unless all three real-tenant variables are set; the unresolved readback limits are listed under Coverage gaps. |
| E2E | Deployed cross-component metering deployment | The deployed metering pipeline and its configured Kafka/provider dependencies | Depends on the installer environment and enabled metering path. |
| Helm render and lint | `make -C osac-metering helm-lint` | Chart templates, the shipped rule revision, and the admission policy that protects it | Deployed behavior. |
| Helm render | `make -C osac-installer metering-auth-render-test` | Metering Deployment, rules ConfigMap, admission policy and binding, and absence of fulfillment credentials in the CI profiles | Does not verify a deployed metering pipeline. |
| Helm unit (reader grants) | `make helm-test` for the reader ACLs and `make readers-render-test` for grant/revoke render stability, plus the `helm unittest` suites for the umbrella and `osac-infra` charts in the helm-unit CI leg | One `KafkaUser` per reader with read and describe ACLs on the heartbeat topic prefix, read on a consumer group named after the reader, and deny ACLs on the built-in tenant topics; duplicate, non-DNS, reserved OSAC and `fulfillment-service` names rejected; the umbrella schema rejects a `tenant` field; the exporter, its PodMonitor and the consumer-lag rule render only when the exporter value is on, the rule selecting heartbeat topics with a value-controlled threshold; the metering Deployment unchanged by a grant or revocation and a removed reader's KafkaUser gone | Does not prove the broker denies a write, that revocation takes effect within 60 seconds, or that Kafka Exporter reports a live reader's lag |

From the repository root, `make -C osac-metering test` runs the full component
suite across schema, metering-service, adapters, and rule revision checks; it is
not a unit-only command. See the `test*` targets in `osac-metering/Makefile`
for the remaining entry points.

## Coverage notes

- **Event schema or usage evaluation:** schema changes affect `schema/`, `metering-service/`, and `adapters/`; run the component `test` target after schema changes.
- **Fold, database, rounds, or relay:** do not skip the real PostgreSQL and Kafka suites; they are the determinism gate for billing.
- **Rule revisions:** the rules suites check that shipped revisions parse, use only tracked fields, never change, and match the chart's meters.
- **Generated contract or published schemas:** the contract tooling under `metering-service/tools/contract` regenerates the committed contract documents and fails on a stale copy; the schema suites validate every published version, its effective date, and that the version metering sends matches the payload; the release-tag comparison script fails on an edit, rename, or deletion of a published file. Never edit a published schema file; ship the next version.
- **Provider adapters:** the shared runner owns ordering, retry, deduplication, and DLQ behavior; the DLQ is mandatory.

## Coverage gaps

- Reader grant, read, revoke, and lag visibility against a deployed Kafka are
  unresolved: `tests/e2e/` has no Kafka client dependency and no helper that
  upgrades installer values to add or remove a reader. The chart renders the
  grant and the lag alert reads Kafka Exporter, but no automated test proves
  the broker refuses a revoked reader within 60 seconds or that a live
  reader's lag reaches Prometheus. No follow-up ticket exists yet.
- The worked example's reading rule is unit-tested; running it against a real
  Kafka topic is unresolved for the same missing Kafka client dependency in
  `tests/e2e/`. The example README documents the manual run.
- The consumer lag PromQL in the `osac-infra` chart is render-tested but not
  validated with `promtool check rules`; a malformed expression would pass
  the chart suites. PromQL linting belongs to a later check, and no
  follow-up ticket exists yet.
- Deployed cross-component journeys (tenant onboarding creating the three
  topics) belong to the `[QE]` E2E work; the event-to-heartbeat path on a hub
  is covered by the metering sanity tier in the vmaas E2E suite.
- The M360 contract test needs a real tenant configured per environment and
  skips when its variables are absent. It verifies submission and synchronous
  acceptance or rejection, but not billed totals: verifying them needs a usage
  query path the adapter does not have, and whether the 48-hour-old heartbeat
  falls in an invoiced period is unverified. This remains unresolved, with no
  specific owner or follow-up ticket identified. The related
  [OSAC-4286](https://redhat.atlassian.net/browse/OSAC-4286) provider E2E epic
  is unassigned and does not assign this readback work. Deploying an M360
  tenant is outside this repository's scope.
