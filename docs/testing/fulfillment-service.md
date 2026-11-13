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
- Fixture-written readiness conditions prove status projection, not automatic
  watch delivery or the upstream controller's calculation of readiness.
- The networking fixture uses distinct Hub entries/namespaces on one cluster;
  it does not establish isolation between separate Kubernetes clusters.
- The MCP protocol suites in `it/` use the real MCP SDK against deployed
  Fulfillment and PostgreSQL; the network provider is omitted, so that
  coverage does not imply a provisioned VM or provider execution. Read the
  fixtures before extending those cases.
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
