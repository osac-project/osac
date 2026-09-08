# OSAC E2E tests

Cross-component pytest suites for VMaaS, CaaS, BMaaS, catalog, storage, and
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

## Validation

- Format and lint changed Python with the repository's configured Ruff checks: `uv run ruff check tests/e2e/` and `uv run ruff format --check tests/e2e/`.
- Collect the affected suite with `uv run pytest --collect-only tests/e2e/<suite>/` before running it.
- Run the narrowest affected test when the required cluster and services are available, for example `uv run pytest tests/e2e/<suite>/<tier>/<test>.py -k '<expression>'`.
- Read a nested suite README, such as [`projects/README.md`](projects/README.md), for service-specific credentials and prerequisites.
