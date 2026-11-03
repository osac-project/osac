# fulfillment-service

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence. Follow this guide for suite setup and limits; inspect test source for
individual cases. Commands and inline paths are relative to `fulfillment-service/`
unless explicitly marked as repository-root commands.

Touched-area requirements: [component guide](../../fulfillment-service/AGENTS.md#integration-tests).

## Test tiers and commands

| Tier | Location / command | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | `internal/`; `ginkgo run -r internal` | Local logic and controlled-client reconcilers | External clients are doubles where the suite declares them. Some packages also start PostgreSQL; inspect the suite setup. |
| Component integration (server/database) | `internal/servers/`; `ginkgo run --timeout 10m internal/servers` | In-process servers and a real PostgreSQL testcontainer | Not a deployed service. Tenancy, attribution, and external clients may be doubles. |
| Component integration (deployed) | `it/`; `make -C osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment` from repository root | Deployed Fulfillment Service, PostgreSQL, authenticated APIs, Kubernetes and the CLI from this checkout | Provider networking is disabled. The `networking-operator` phase additionally deploys the current operator; other cases do not establish operator/provider behavior. |
| Component integration (identity) | Service-account and RoleBinding suites in `it/`, run through the deployed target above | Fulfillment, PostgreSQL, Keycloak and configured runtime identities | Failure-injection suites can run the service/reconciler in process. Inspect their setup before claiming deployed controller behavior. |
| E2E | `../tests/e2e/` | Deployed cross-component user journeys | Requires the corresponding operators, AAP and providers. |

## Selecting coverage

- CLI/API-only contracts belong in `it/`; operator and provider journeys belong
  in E2E. Use the suite setup and skip conditions to identify the actual boundary.
- Tests behind `ndSkipUnlessProviderNetworkingEnabled` skip in the standard
  Fulfillment target. Their presence is not evidence for the enabled-provider path.
- Fixture-written readiness conditions prove status projection, not automatic
  watch delivery or the upstream controller's calculation of readiness.
- The networking fixture uses distinct Hub entries/namespaces on one cluster;
  it does not establish isolation between separate Kubernetes clusters.
- MCP protocol coverage lives in `it/it_mcp_server_test.go`. Read its fixture
  before extending discovery, authorization or lifecycle cases; protocol and
  API/database coverage do not imply a provisioned VM or provider execution.
- FabricDomain setup and focused execution are documented in
  [the suite README](../../fulfillment-service/it/README.fabric-domain.md).
- Derive individual scenarios, expected results and focused filters from test
  source and approved requirements; keep this guide at suite level.

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

## Fulfillment integration timings

The `SUITE=fulfillment` installer target uses Ginkgo's native `--json-report`
flag when `FULFILLMENT_IT_REPORT_DIR` is set. The default is empty: local runs
neither generate nor clear reports. To opt in from the repository root, run:

```bash
make -C osac-installer test PLATFORM=kind PROFILE=dev NS=osac SUITE=fulfillment FULFILLMENT_IT_REPORT_DIR=../fulfillment-service/logs/timings
```

Relative directories resolve from `osac-installer/`. CI sets an absolute
`FULFILLMENT_IT_REPORT_DIR` under `fulfillment-service/logs/timings/`, which its
existing `logs` artifact includes. The three phases write `service-only.json`,
`networking-operator.json`, and `bmaas-disabled.json`. Only the
networking-operator phase deploys the OSAC Operator.
When reporting is enabled, those three prior files are cleared before the run
so a reused local directory cannot retain stale reports for unreached phases.
A missing report does not imply a passing phase. Use the originating Actions
run to identify the tested revision.

These reports are for maintainers investigating CI runtime or comparing fixture
costs before and after an optimization. Download the artifact and inspect its
native Ginkgo suite and spec reports. Total runtime, status, attempts, seed,
suite configuration, and node events support analysis outside the test suite.
For example, list executed specs from a downloaded report with:

```bash
jq '.[] | .SpecReports[]
    | select(.LeafNodeType == "It" and .State != "skipped" and .State != "pending")
    | {name: ((.ContainerHierarchyTexts + [.LeafNodeText]) | join(" ")),
       seconds: (.RunTime / 1000000000)}' service-only.json
```

Native reports include captured output, failures, and report entries, and
should be handled as CI diagnostic logs. They are not sanitized timing-only
summaries. Ginkgo documents the format in
[machine-readable reporting](https://onsi.github.io/ginkgo/#generating-machine-readable-reports).

Fixtures called inside an `It` remain part of its body; node timings do not
separate that fixture work. Build, deployment and test compilation time are
outside suite runtime; use Actions step timestamps and build logs for those
costs. Runtime savings require comparable deployed runs and are not established
by unit tests.
