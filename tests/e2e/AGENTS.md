# OSAC E2E tests

Cross-component pytest suites for VMaaS, CaaS, BMaaS, storage, and
resource-reference workflows.

This test suite is part of the OSAC monorepo, not an isolated project. Its
scenarios, fixtures, and shared helpers may affect other components. Apply the
repository-wide rules in [`../../AGENTS.md`](../../AGENTS.md), consider downstream
effects, and follow the instructions for every affected component.

## Invariants

- E2E suites live only under `tests/e2e/`, not in the external `osac-test-infra` checkout; that repository owns infrastructure backends, not suites.
- Put a test in the service-tier directory that owns its user journey and preserve the pytest markers used by CI.
- Avoid fixed sleeps; wait for observable resource conditions with bounded timeouts.
- Tests clean up resources they create and never assume the caller's shared-cluster namespace or context.
- Shared fixture changes require collecting all affected suites.

## Touched-area map

| Area | Required coverage | Location and command | Boundary and prerequisites |
|---|---|---|---|
| VMaaS ComputeInstance InstanceType resize, including CatalogItem-created instances | Regression E2E for CLI/API outcomes, applied ComputeInstance configuration, and VMI resources | `tests/e2e/vmaas/regression/test_compute_instance_instance_type.py`; from the repository root, run `uv run pytest tests/e2e/vmaas/regression/test_compute_instance_instance_type.py` | Uses the deployed VMaaS API and Kubernetes endpoints, VM kubeconfig, image, storage tier, subnet, and VM template. Restart-required behavior assumes the single-node VMaaS profile. |
| CaaS deletion helpers and focused teardown ordering | [DEV] Unit regressions for exact NotFound, parent-before-dependent ordering, polling budgets, safe diagnostics, and no mutation | From the repository root: `uv run pytest -n 0 tests/unit/test_caas_teardown_order.py tests/unit/test_cluster_deletion_polling.py tests/unit/test_caas_deletion_diagnostics.py tests/unit/test_caas_worker_bmi_visibility.py tests/unit/test_caas_two_node_sets.py tests/unit/test_caas_selector_contracts.py` | Kubernetes, fulfillment responses, and time are mocked; selector regressions cover NodeSet identity with shared BMITs and shared-only BMIT reference expectations. Does not prove deployed reconciliation. |
| Focused bare-metal CaaS natural teardown | [QE] E2E for worker BMI removal, natural ClusterOrder removal, independent InfraEnv GC, fulfillment removal, and deleted metering | From the repository root: `uv run pytest -n 0 tests/e2e/caas/sanity/test_cluster_create.py::test_cluster_create --junitxml=/tmp/test-output/caas-bm-teardown-junit.xml` | Requires compatible source-pinned OSAC/operator/installer/AAP images and project source, hub access/auth, cluster template/release/disk images, pull-secret/SSH-key inputs, available virtual BMHs, and healthy Kafka/metering. No teardown completion is mocked. |
| FabricDomain Phase 1 API authorization and tenant visibility | Auth Unit plus deployed API/component integration; not live fabric lifecycle E2E | `go test ./internal/auth` from `fulfillment-service/`; `uv run pytest -n 0 tests/e2e/bmaas/regression/networking/test_fabric_domain_api.py` from the root | Requires explicit API-only opt-in, a fully rolled-out operator with networking provisioning disabled, and an Ethernet east-west capable Netris NetworkClass. See [prerequisites and pending lifecycle coverage](bmaas/regression/networking/README.fabric-domain.md). |

The `test_cluster_create` and `test_cluster_create_with_two_node_sets` scenarios
share natural teardown assertions without the legacy helper's forced cleanup.
The two-node-set scenario verifies ready worker aggregates, installed Agents,
and per-NodeSet NodePool isolation before deleting; it uses the same
source-pinned environment prerequisites as the focused lifecycle. Run it with
`uv run pytest -n 0 tests/e2e/caas/regression/test_cluster_node_sets.py::test_cluster_create_with_two_node_sets`.
CaaS sanity contains only the single-nodepool lifecycle and the fast deletion
feedback test. Two-node-set isolation and explicit/invalid version cases belong
in CaaS regression; version cases are consolidated in
`regression/test_cluster_version.py` rather than duplicated in sanity.
Other CaaS scenarios still using `wait_for_cluster_deletion` do not establish
that boundary. Both scenarios verify tenant/owner annotations on their worker
BMIs and wait for all verified workers to disappear (480 attempts at five-second
intervals). Only then does the parent wait start, with 121 attempts at
ten-second intervals, followed by
60 InfraEnv attempts at five-second intervals. These are separate polling
budgets (1,200 and 295 seconds of scheduled sleeps), not wall-clock deadlines or
production SLAs; API calls and diagnostics add time. Sanitized snapshots are
sampled approximately every sixty seconds and once on either stage's timeout.
See [integration-testing boundaries](../../docs/INTEGRATION-TESTING.md#testse2e)
for real dependencies and omitted hardware/storage coverage.

Multi-node live hot-plug resize remains outside this suite and is tracked by
[OSAC-5335](https://redhat.atlassian.net/browse/OSAC-5335).

## Fulfillment trust enablement

Set `OSAC_FULFILLMENT_TRUST_E2E=true` to add trust assertions to the CaaS,
CSI storage, VMaaS, and BMaaS journeys. The CaaS and CSI checks read the
HostedControlPlane kubeconfig Secret and pass it to `kubectl` through stdin
without writing it to disk. This kubeconfig provides tenant-admin access to
verify the tenant ConfigMap and CSI Deployment hashes against the management
bundle; per-ClusterOrder observer credentials are not required. Run the
installer hook check in `enablement/test_installer_trust.py` against a full
OSAC Helm release. Dev and CI profile values disable trust by default; enable
trust explicitly when deploying the release E2E environment.

Run `enablement/test_ca_rotation_gate.py` with
`OSAC_TRUST_ROTATION_PHASE=overlap` and the candidate
`OSAC_TRUST_ROTATION_EXPECTED_HASH` before selecting the new leaf certificate.
Run it with `OSAC_TRUST_ROTATION_PHASE=post-switch` before removing the retired
root, and with `OSAC_TRUST_ROTATION_PHASE=final` after removal. All runs require
deployed operator and metering metrics and separate old/new root PEM files for
leaf verification.
The rotation test reads resources and metrics; it does not mutate CA or leaf
certificate resources.

## Validation

- Format and lint changed Python with the repository's configured Ruff checks: `uv run ruff check tests/e2e/` and `uv run ruff format --check tests/e2e/`.
- Collect the affected suite with `uv run pytest --collect-only tests/e2e/<suite>/` before running it.
- Run the narrowest affected test when the required cluster and services are available, for example `uv run pytest tests/e2e/<suite>/<tier>/<test>.py -k '<expression>'`.
- Read a nested suite README, such as [`projects/README.md`](projects/README.md), for service-specific credentials and prerequisites.
