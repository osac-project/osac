# CaaS integration tests

These Kind tests exercise Fulfillment, the OSAC operator, BMF and AWX together.
External provider controllers and hardware are simulated. No physical servers
or guest OpenShift cluster are required.

## Code layout

- `suites/cluster/` — Integration cases and nearby test fixtures.
- `suites/fixtures/` — Shared application setup, correlation and assertions.
- `internal/harness/` — Native Kubernetes operations, inventory and ignition I/O.
- `internal/awx/` — Automation configuration and execution verification.
- `internal/simulator/` — Native provider models and their unit tests.
- `charts/` — API/AWX bootstrap and provider deployment.

## Architecture and safety

- Keep each component's implementation and tests in its simulator package.
  Write only resources and fields its real counterpart owns.
- Providers run and recover independently of the test runner and OSAC APIs.
  Keep test IDs and suite helpers out of provider behavior.
- Use native references, watches and status feedback. Do not call another
  reconciler or depend on reconciliation order.
- Do not add test-only coordination annotations or private progress stores.
- Only `scenario` interprets case controls and profiles. Consumers define narrow
  interfaces and must not import `scenario`.
- `host` handles native boot, registration and release—not tenant/allocation/slot
  correlation, Agent approval or consumer selection.
- Shared Kubernetes helpers provide operations, not component policy.
  Setup wires components without implementing their behavior.
- Preserve native ownership and namespace/UID/version checks in simulators.
  Recovery and cleanup must respect the provider's resource associations and
  preserve objects outside them. Enforce OSAC tenant and request ownership in
  the application and suite fixtures, not in provider reconcilers.
- Other components must build and run their checks without this module or
  its workspace.

## Provider fidelity and integration boundaries

- Base each simulator's selection, writes, status and deletion on its real
  upstream controller at the version used by this module. Check the upstream
  implementation before changing behavior; use the native resource contract
  where full execution is impossible without a guest cluster.
- Do not use OSAC labels, tenant annotations, order identities or playbook
  selectors to decide what a provider reconciles unless its real controller
  uses them. A provider must not import or interpret OSAC application policy.
- Keep OSAC policy in the real application components. Suite fixtures assert
  that those components create appropriate provider inputs and clean up the
  resources they select.
- If upstream provider behavior and OSAC's selectors differ, test each at its
  own boundary and report the interaction. Do not change the simulator to
  conceal the difference.

## Documentation

- Only targeted corrections to existing factual errors in `AGENTS.md` and `README.md` are allowed; additions, explanations, clarifications are prohibited.

## Code quality

- Write simple, idiomatic Go with clear names and readable conditions.
- Keep functions focused on a component responsibility or complete lifecycle step.
  Extract helpers for clarity, not merely to satisfy complexity checks.
- Avoid unnecessary abstractions, generic reconciliation frameworks, callback
  pipelines, parameter bags and trivial wrappers.
- Reuse suitable code and helpers; preserve additional ownership, UID, namespace
  and concurrency checks.
- Preserve lifecycle ordering, recovery paths and optimistic locking.
- Document every named function and method, including helpers and tests.
  Explain purpose and important constraints in plain language.
- Comments explain current intent and non-obvious decisions. Avoid jargon,
  historical comparisons and obvious restatements. Keep useful comments with
  their code when moving it.
- Keep changes focused and preserve unrelated or parallel edits.

## Writing tests

- Use Ginkgo/Gomega for unit and deployed tests.
- Keep unit tests beside implementations under `internal/`. Use fake clients,
  local HTTP servers and temporary files; no cluster, deployed services or envtest.
- Keep Ginkgo/Gomega out of runtime implementations. Assertions belong in tests
  and suite fixtures; runtime operations return errors.
- Use one unit-suite bootstrap per package, fresh per-spec fixtures and `DeferCleanup`.
  Use `DescribeTable` for independent variations; keep lifecycle steps in one `It`.
- Test cross-component behavior through the owning components.
- Keep cases independent: no `Ordered`, `Serial`, cross-case state or Ginkgo flake retries.
- Use bounded polling and register cleanup when creating resources. Use `By` for
  steps and keep polling callbacks focused on one observation.
- Integration cases may use case controls and bounded host operations,
  but must not call native reconciler implementations.
- Register Agents only after real BMF/AWX provisioning.
- Require natural cleanup and successful AWX attempts. Remove case settings last.
- Never force a pass through status repair, application/foreign finalizer removal,
  persistent inventory deletion or weakened assertions.

## Checks and execution

From this directory or `make -C tests/integration/caas <check>` from root:

- `make check` — Static and chart checks; also the default target.
- `make lint` — Go lint checks. Run after finished modifying `.go` files, before running tests.
- `make lint-fix` — Formatting and automatic fixes; review the changes.
- `make unit` — Internal unit tests.

Prefer focused checks relevant to the change.
Follow the [README](README.md) for setup and deployed tests.
Use the same cluster name, runtime and kubeconfig throughout.

Run one suite invocation at a time. Parallel workers need disjoint inventory,
HTTP fixtures and case settings. Keep image loading, deployments, Helm updates
and manual controls outside test runs.

`destroy` deletes the entire selected Kind cluster.
