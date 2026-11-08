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
| Component integration (deployed) | The deployed `it/` suite, run through the installer integration target with the fulfillment suite selected | Deployed Fulfillment Service, PostgreSQL, authenticated APIs, Kubernetes, and the CLI from this checkout | Provider networking is disabled. Only the `networking-operator` phase deploys the current operator; other cases do not establish operator/provider behavior. |
| Component integration (identity) | Service-account and RoleBinding suites in `it/`, via the deployed target above | Fulfillment, PostgreSQL, Keycloak, and configured runtime identities | Failure-injection suites can run the service/reconciler in process; inspect their setup before claiming deployed controller behavior. |
| E2E | `../tests/e2e/` | Deployed cross-component user journeys | Requires the corresponding operators, AAP, and providers. |

For commands, see the
[component guide](../../fulfillment-service/AGENTS.md#integration-tests) and
run `make -C osac-installer help` for the installer targets that drive the
deployed suite.

## Selecting coverage

- CLI/API-only contracts belong in `it/`; complete provisioning journeys across
  operators and providers belong in E2E. Use the suite setup and skip
  conditions to identify the actual boundary.
- Tests gated on provider networking skip in the standard fulfillment target;
  their presence is not evidence for the enabled-provider path.
- Fixture-written readiness conditions prove status projection, not automatic
  watch delivery or the upstream controller's calculation of readiness.
- The networking fixture uses distinct Hub entries/namespaces on one cluster;
  it does not establish isolation between separate Kubernetes clusters.
- The MCP protocol suite in `it/` covers discovery, authorization, and
  lifecycle; that coverage does not imply a provisioned VM or provider
  execution. Read its fixture before extending those cases.
- FabricDomain setup and focused execution are documented in
  [the suite README](../../fulfillment-service/it/README.fabric-domain.md).

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
default). One report is written per phase and a reused directory is cleared
first; a missing report does not imply a passing phase. The reports are CI
diagnostics for maintainers investigating suite runtime or fixture costs: they
include captured output and failures, not only timings. Fixtures called inside
an `It` remain part of its body, build and deployment time sit outside suite
runtime, and runtime savings require comparable deployed runs, not unit tests.
