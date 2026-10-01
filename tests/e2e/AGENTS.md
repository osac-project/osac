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
| FabricDomain Phase 1 API authorization and tenant visibility | Auth Unit plus deployed API/component integration; not live fabric lifecycle E2E | `go test ./internal/auth` from `fulfillment-service/`; `uv run pytest -n 0 tests/e2e/vmaas/regression/test_fabric_domain_api.py` from the root | Requires explicit API-only opt-in, a fully rolled-out operator with networking provisioning disabled, and an Ethernet east-west capable Netris NetworkClass. See [prerequisites and pending lifecycle coverage](vmaas/regression/README.fabric-domain.md). |

Multi-node live hot-plug resize remains outside this suite and is tracked by
[OSAC-5335](https://redhat.atlassian.net/browse/OSAC-5335).

## Validation

- Format and lint changed Python with the repository's configured Ruff checks: `uv run ruff check tests/e2e/` and `uv run ruff format --check tests/e2e/`.
- Collect the affected suite with `uv run pytest --collect-only tests/e2e/<suite>/` before running it.
- Run the narrowest affected test when the required cluster and services are available, for example `uv run pytest tests/e2e/<suite>/<tier>/<test>.py -k '<expression>'`.
- Read a nested suite README, such as [`projects/README.md`](projects/README.md), for service-specific credentials and prerequisites.
