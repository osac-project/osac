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
