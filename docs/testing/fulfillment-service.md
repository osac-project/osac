# fulfillment-service

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence. Inspect test source for individual cases. Paths are relative to
`fulfillment-service/` unless marked as repository-root paths.

Touched-area requirements: [component guide](../../fulfillment-service/AGENTS.md#integration-tests).

## Test tiers

| Tier | Suite (by directory/role) | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | Go suites under `internal/` | Local logic and controlled-client reconcilers | External clients are doubles where the suite declares them; some packages also start PostgreSQL. |
| Component integration (server/database) | `internal/servers` | In-process servers and a real PostgreSQL testcontainer | Not a deployed service; tenancy, attribution, and external clients may be doubles. |
| Component integration (deployed) | The deployed `it/` suite, run through the installer integration target with the fulfillment suite selected | Deployed Fulfillment Service, PostgreSQL, authenticated APIs, Kubernetes, and the CLI from this checkout | Provider networking is disabled. Only the `networking-operator` and `compute-storage` phases deploy the current operator; other cases do not establish operator/provider behavior. |
| Component integration (identity) ([DEV] [OSAC-6553](https://redhat.atlassian.net/browse/OSAC-6553), [OSAC-6554](https://redhat.atlassian.net/browse/OSAC-6554), [OSAC-6555](https://redhat.atlassian.net/browse/OSAC-6555), [OSAC-6556](https://redhat.atlassian.net/browse/OSAC-6556)) | Service-account, RoleBinding, and service-account CLI suites in `it/`, via the deployed target above | Fulfillment, PostgreSQL, Keycloak, and the configured runtime identities | Failure-injection cases run the service and reconciler in process against PostgreSQL and Keycloak; they do not exercise the deployed controller loop, and external provider provisioning is out of scope. |
| E2E | `../tests/e2e/` | Deployed cross-component user journeys | Requires the corresponding operators, AAP, and providers. |

For commands, see the
[component guide](../../fulfillment-service/AGENTS.md#integration-tests) and
run `make -C osac-installer help` from the repository root for the installer targets that drive the
deployed suite.

## ComputeInstance disk handoff (OSAC-6685)

The `compute-storage` component-integration phase runs through the installer
integration target with real Fulfillment APIs, PostgreSQL, the installed
storage and ComputeInstance controllers, and Kind StorageClass lookup. AAP is
a recording double; no provider, CSI, or VM execution. The operator-side
boundary is in the [osac-operator guide](osac-operator.md); deployed disk
identity E2E is [OSAC-6614](https://redhat.atlassian.net/browse/OSAC-6614).

## Selecting coverage

- CLI/API-only contracts belong in `it/`; complete provisioning journeys across
  operators and providers belong in E2E. Use the suite setup and skip
  conditions to identify the actual boundary.
- Tests gated on provider networking skip in the standard fulfillment target;
  their presence is not evidence for the enabled-provider path.
- ExternalIPPool tenant placement: a pool created without a tenant is stored
  as shared, any other tenant is rejected without persisting, a shared pool
  cannot move into a tenant, and a pool stored in a tenant before this rule
  keeps it and still takes status updates. Unit specs use real PostgreSQL with
  mocked tenancy; the deployed private-API rejection does not depend on
  provider networking.
- ExternalIP finalizer retry signaling ([DEV] OSAC-5977): controller unit
  suites cover retry after a transient signal failure and stopping on `NotFound`,
  and verify deletion with provider cleanup pending schedules another signal
  while the Kubernetes finalizer remains. They use mocked gRPC clients; deployed
  provider cleanup stays covered by the OSAC-6307 operator contract tests and
  the OSAC-4843 E2E follow-up.
- Fixture-written readiness conditions prove status projection, not automatic
  watch delivery or the upstream controller's calculation of readiness.
- The networking fixture uses distinct Hub entries/namespaces on one cluster;
  it does not establish isolation between separate Kubernetes clusters.
- The MCP protocol suites in `it/` use the real MCP SDK against deployed
  Fulfillment and PostgreSQL; the network provider is omitted, so that
  coverage does not imply a provisioned VM or provider execution. Read the
  fixtures before extending those cases.
- MCP write safety ([OSAC-5851](https://redhat.atlassian.net/browse/OSAC-5851))
  uses typed client failure injection, a pass-through token exchanger, deployed
  Fulfillment, and PostgreSQL. Its unit suite also uses the generated public
  gRPC client and an in-process server to cover a successful Create whose
  response exceeds the client receive limit. Broader transport faults and
  host-driven stop behavior remain with
  [OSAC-5852](https://redhat.atlassian.net/browse/OSAC-5852). Provider
  provisioning is out of scope.
- FabricDomain setup and focused execution are documented in
  [the suite README](../../fulfillment-service/it/README.fabric-domain.md).
- Deployed service-account journeys, secret rotation included, are the
  [OSAC-6557](https://redhat.atlassian.net/browse/OSAC-6557) [QE] regression
  suite in `tests/e2e/` (see the [tests/e2e guide](e2e.md)). The UI
  walkthrough remains with the separately owned UI implementation.
- Omitted network-attachment defaulting
  ([OSAC-6480](https://redhat.atlassian.net/browse/OSAC-6480), DEV) is
  covered by deployed public-API cases and unit coverage in the servers
  suites; provider provisioning remains E2E coverage.
- Default networking rejection
  ([OSAC-6601](https://redhat.atlassian.net/browse/OSAC-6601),
  [OSAC-7044](https://redhat.atlassian.net/browse/OSAC-7044), DEV): creates
  with a missing or not-ready tenant default are rejected in the private
  servers suite and the deployed public Create; provider provisioning remains
  E2E coverage.
- Quota tables and APIs ([DEV] [OSAC-7613](https://redhat.atlassian.net/browse/OSAC-7613)):
  the five quota tables are covered by
  `internal/database/migrations/153_create_quota_tables_test.go` (per-scope
  dimension uniqueness including pending deletion, identity and ownership
  immutability, tenant and project foreign keys, the one-pending-request rule,
  the changes-table audit of limit changes, the outbox's tenant and project
  references, and the active companions); the concurrent link indexes the
  usage counter nets against, the volumes name-pattern index and the
  bare-metal instances cluster-link index (migrations 154 and 155), by their
  own migration tests; the
  quota limit, default limit, threshold, limit request and usage services by
  `internal/servers/quota_servers_test.go` against real PostgreSQL; and the
  registry-driven usage counter by `internal/quotausage/counter_test.go`
  against real resource rows, including the larger-of rule for a mid-resize
  VM, created resources counting in their own kind with the parent adding
  only the excess, and a stored reference resolving its type by the scope it
  carries, so a same-name type of another scope can't replace it. The same
  suite covers the counting rules (the release map
  of the quota state standard): rows in a mapped state stop counting, an
  empty default changes nothing, rules load from the file named by
  `OSAC_QUOTA_COUNTING_RULES`, a missing file means no rules, and rules the
  counter can't apply fail the build. The tenant and project reconcilers'
  quota-row cleanup, and the outbox rows the tenants and projects servers
  remove with an accepted delete, are covered by the controllers suites and
  `internal/servers`. Tenancy and attribution are suite
  doubles; the reviewed reader grant of `QuotaUsage/Get` is asserted in
  `internal/auth`, and the role grants are exercised through the server
  specs' visibility doubles, which check the servers enforce each role's
  visibility. Deployed REST or CLI journeys for the quota APIs, the
  Approve/Deny path under a deployed Cloud Provider Admin identity, and
  enforcement and warning publishing are not exercised; enforcement is
  [OSAC-7614](https://redhat.atlassian.net/browse/OSAC-7614).

- **StorageTier/Volume reference guard ([OSAC-7049](https://redhat.atlassian.net/browse/OSAC-7049), DEV):** `internal/cmd/service/start/grpcserver`, `internal/servers` and `internal/database/migrations` exercise production DAOs and in-process handlers against temporary PostgreSQL, including concurrent access. Tenancy and attribution are controlled; deployed service, operator and provider behavior is not covered.

## Coverage gaps

- Provider-enabled networking and FabricDomain-to-Netris lifecycle need their
  deployed environments. The latter is owned by
  [OSAC-4786](https://redhat.atlassian.net/browse/OSAC-4786).
- Storage feedback fixtures do not prove AAP, CSI, provider cleanup, LVMS
  placement or pending LogicalVolume cleanup. The deployed storage placement
  and pending-volume cleanup coverage remains unresolved; the existing
  [OSAC-6614](https://redhat.atlassian.net/browse/OSAC-6614) /
  [OSAC-6615](https://redhat.atlassian.net/browse/OSAC-6615) work does not
  explicitly require an LVMS tier.
- ComputeInstance Volume tests currently use a placeholder `osac.io/node`;
  the accepted [OSAC-6037](https://redhat.atlassian.net/browse/OSAC-6037) design
  still specifies unset topology. Passing current tests does not resolve that
  contract difference. Cross-component completion/cleanup belongs to
  [OSAC-6040](https://redhat.atlassian.net/browse/OSAC-6040); its QE suite path
  and command remain unspecified.
- Existing-token revocation is separate from fresh-login RoleBinding checks:
  [OSAC-6375](https://redhat.atlassian.net/browse/OSAC-6375).

## Fulfillment integration reports

The deployed fulfillment suite can emit native Ginkgo JSON reports, gated on
the `FULFILLMENT_IT_REPORT_DIR` variable in the installer Makefile (empty by
default). One report is written per phase; the fulfillment selection writes
four phase reports and the compute-storage selection runs only its own phase.
Only the selected suite's report files are cleared before the run, so a
reused local directory cannot retain stale reports for unreached phases; a
missing report does not imply a passing phase. The reports are CI
diagnostics for maintainers investigating suite runtime or fixture costs: they
include captured output and failures, not only timings. Fixtures called inside
an `It` remain part of its body, build and deployment time sit outside suite
runtime, and runtime savings require comparable deployed runs, not unit tests.

The same make targets also emit Ginkgo JUnit reports, gated on the
`JUNIT_REPORT_DIR` variable (empty by default, in both the installer and the
CaaS integration Makefiles): one `<phase>.xml` file per phase, with the same
stale-report clearing as the JSON reports. CI sets the variable and merges the
phase files into the single `junit.xml` at the root of the run's
`osac-logs-it-<suite>-<run_id>` log bundle, which the ci-observe monitoring
reads; the phases of one suite share Ginkgo's suite description, so each
merged `<testsuite>` name carries the phase file's stem.
