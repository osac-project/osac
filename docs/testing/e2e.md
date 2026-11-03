# tests/e2e

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence. Follow this guide for suite setup and limits; inspect test source for
individual cases. Commands and inline paths are relative to the repository root.

Touched-area requirements and suite commands:
[component guide](../../tests/e2e/AGENTS.md#touched-area-map).
Read `tests/e2e/conftest.py`, suite fixtures and skip conditions before extending
or claiming coverage. Unit tests under `tests/unit/` validate helpers and selectors;
pytest collection proves import/selection readiness only.

## Suite boundaries

| Suite / command | Real dependencies and limits |
|---|---|
| CaaS: `uv run pytest -n 0 tests/e2e/caas/sanity/test_cluster_create.py::test_cluster_create` | Fulfillment/database, Kubernetes, operators, AAP, HyperShift/CAPI/CAP-Agent, Assisted Service and Kafka/metering. Provider-backed virtual BMHs do not prove physical hardware, guest LVMS readiness, CSI mount or application I/O. |
| VMaaS/BMaaS/storage/reference suites | Use the commands and environment gates in the component touched-area map and suite fixtures. Provisioning requires compatible deployed templates, images, providers and credentials. Current storage E2Es establish orchestration/StorageClass setup, not a complete CSI PVC-to-mounted-workload path. |
| Metering: `METERING_ADAPTER_URL=<echo adapter URL> uv run pytest tests/e2e/vmaas/sanity/test_metering_billing.py` | Deployed event-to-heartbeat path through API/Kafka/metering and echo adapter. ExternalIP billing belongs to the networking billing suite. |
| Netris VMaaS: `uv run pytest -n 0 -x tests/e2e/networking/vmaas/test_vmaas_networking.py` | Real Fulfillment/operator/AAP/KubeVirt/CUDN EVPN/Netris. Requires `vmaas-ci` with `osac-installer/values/vmaas-ci/netris.yaml`, VM kubeconfig, `kubectl`, a pullable SSH helper image and a pytest host routing `OSAC_EXTERNAL_IP_POOL_CIDR`. Numbered tests are serial. |
| Networking billing: `METERING_ADAPTER_URL=<echo adapter URL> uv run pytest -n 0 -x tests/e2e/networking/vmaas/test_external_ip_billing.py tests/e2e/networking/vmaas/test_external_ip_reattach_billing.py tests/e2e/networking/vmaas/test_nat_gateway_billing.py` | Requires provider networking enabled, routed pool and echo adapter; skips give no billing signal without the adapter. Runs separately after connectivity even if that module fails; either failure fails the job. No traffic or SSH helper is needed. |
| Netris BMaaS: `uv run pytest -n 0 tests/e2e/networking/bmaas` | Requires BMH SSH access and `OSAC_BMI_NETWORK_INTERFACE`. Its PR job omits slow cross-tenant cases; the Netris `full` suite excludes this directory because cudn_evpn rejects its ICMP rules. |
| Stretched: `uv run pytest -n 0 tests/e2e/networking/stretched` | BMI/VM on one Subnet; needs `DEPLOY_SUITE=full TEST_SUITE=stretched`, BMaaS SSH inputs, VM template/helper and EVPN underlay. Not part of standard VMaaS/BMaaS runs. |

## Coverage gaps

- VM-to-VM inter-subnet routing across CUDNs is out of scope for Phase 1 and
  tracked for Phase 2 by [OSAC-3667](https://redhat.atlassian.net/browse/OSAC-3667).
- The Netris VMaaS suite under [OSAC-4291](https://redhat.atlassian.net/browse/OSAC-4291)
  does not cover the ICMP-capable AAP path or the combined second-tenant
  automatic ExternalIP ingress and garbage-collection journey. No follow-up
  owner or ticket has been identified for these two E2E gaps; both remain
  unresolved.
- Pool capacity, attachment validation and name-reference cases have lower-tier
  tests in Fulfillment. They do not replace provider release or cross-component
  billing evidence. Provider-enabled Fulfillment specs skip in its standard
  provider-disabled deployment; read their skip conditions before citing them.
- The AgentlessNet DHCP lab is a separately source-pinned environment under
  [OSAC-5530](https://redhat.atlassian.net/browse/OSAC-5530) and
  [OSAC-1562](https://redhat.atlassian.net/browse/OSAC-1562). It does not prove
  physical hardware, full BMI provisioning/status, NAT or full-install CI.
- Multi-node live hot-plug remains
  [OSAC-5335](https://redhat.atlassian.net/browse/OSAC-5335).

## CaaS CI selection boundary

The full-install callers in osac and osac-test-infra select `caas/sanity` with
`-m sanity` for PRs and merge queue. Test-infra dispatches carrying `pr-number`
use the same policy regardless of tier labels or suite/marker overrides.
Periodics select all of `caas` (sanity + regression) plus reference tests marked
`requires_caas` or `reference_common`. Manual dispatches without PR context
default to sanity and retain suite/filter/marker overrides. The reusable runner
never adds references to sanity; an empty requested tier fails rather than
falling back to the whole suite. Nightly/release validation already selects
`caas` explicitly and remains unchanged.

[DEV] Unit/Contract selection checks live in
`tests/unit/test_caas_ci_suite_policy.py`. From the repository root, run
`OSAC_TEST_INFRA_DIR=/path/to/osac-test-infra uv run pytest -n 0 tests/unit/test_caas_ci_suite_policy.py`.
They evaluate actual caller YAML and execute path-selection Bash against
local temporary directories. Companion-repository checks skip when the path
is not set. These checks and pytest collection prove selection only, not
[QE] deployed CaaS success; the live prerequisites below still apply.

## Focused CaaS natural-teardown boundary

Sanity uses a published ClusterCatalogItem. The two-node-set scenario is in
`tests/e2e/caas/regression/test_cluster_node_sets.py` and needs enough BMHs for
both sets. Sharing a BMIT across sets remains unit coverage, not a deployed claim.
The natural teardown path verifies worker ownership, waits for the exact BMIs,
then ClusterOrder, then independent InfraEnv absence. API errors, terminating
objects and empty status are not absence. It removes no lifecycle hooks or
finalizers; legacy cleanup-enabled waits do not prove natural teardown.
Read the helpers for budgets and diagnostics; retry sleep budgets are not
production SLAs. Preserve JUnit, lifecycle and cleanup evidence before teardown.

Use `tests/e2e/conftest.py` for hub kubeconfig/namespace, API/auth, CLI/utilities,
image/template/release, pull-secret/SSH, virtual BMH and Kafka/metering inputs.
Pin compatible OSAC/operator/installer/AAP revisions and image digests; AAP must
resolve the tested revision, not mutable `main`/`latest`.
Guest LVMS disk prerequisites remain a separate infrastructure follow-up with
unresolved revision, owner and Jira URL. Do not wipe/reuse the guest OS disk.
