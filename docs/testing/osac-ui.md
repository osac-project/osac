# osac-ui

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence. Inspect test source for individual cases. Paths are relative to
`osac-ui/` unless marked as repository-root paths.

Touched-area requirements: [component guide](../../osac-ui/AGENTS.md#test).

## Test tiers

| Tier | Suite (by directory/role) | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | Vitest suites in `apps/app-frontend/src` and `libs/ui-components/src` (`pnpm test`) | Components, hooks, and API wiring with an in-memory Connect transport | No browser, no deployed proxy, no fulfillment service |
| UI E2E (manual) | Playwright flows in `apps/e2e-tests` (`src/vmaas/`, `src/networking/`) driven by the harness in `apps/playwright` | Real browser through the deployed SPA, Go proxy, Keycloak OIDC login, and fulfillment-service: VMaaS resource and VM journeys, External IP pool and tenant allocation | Nothing is mocked; the suite needs a deployed environment plus admin and tenant credentials, so it never runs in CI |

`pnpm run typecheck`, `pnpm lint` and `pnpm test` are separate CI jobs and the
unit tier gate for this component. The Playwright flows are manual: see
[`apps/e2e-tests/README.md`](../../osac-ui/apps/e2e-tests/README.md).

## Coverage notes

- **Unit tier:** mock Connect traffic with `createMockConnectTransport`; do not
  mock fetch or REST endpoints.
- **UI E2E tier:** every flow logs in with real credentials, mutates a real
  deployment, and must clean up in the same run. Give resources unique,
  run-indexed names and let the Playwright teardown projects remove them.
- **External IP flows:** the pool project provisions the pool the tenant flows
  allocate from unless `OSAC_EXT_IP_POOL` names an existing one. Allocation
  depends on the configured CIDR being usable on the target cluster, so a
  cluster that cannot route the CIDR surfaces as `Failed` rather than
  `Allocated`.
- **Provider networking must be enabled.** The fulfillment service rejects
  External IP creates with `FailedPrecondition: provider networking is disabled`
  when `global.networking.provisioningEnabled` is `false`, and the stock
  `osac-installer/values/dev/kind-instance.yaml` profile sets it to `false`.
  On such a deployment only the list and error-state checks can pass.
- **VMaaS flows:** the full suite creates tenants, roles, storage, catalog and
  VM resources; run its prerequisites before the tenant-scoped flows.

## Coverage gaps

- **No CI execution for UI E2E.** Nothing in `.github/workflows`, `.tekton` or
  `osac-test-infra` runs Playwright, and the agentic CI sandbox classifies
  `pnpm playwright` as a manual live-cluster harness. Pipeline gating of UI
  coverage is tracked separately by
  [OSAC-4847](https://redhat.atlassian.net/browse/OSAC-4847); this component's
  flows are verified by manual runs recorded in the owning task or PR.
- **External IP attachment paths are untested.** Attach and detach run through
  the VM, bare-metal, cluster and NAT gateway flows; no UI flow covers an
  External IP moving to `In use`, which also leaves the "detach before deleting"
  guard unexercised.
- **Cross-tenant denial is asserted from the tenant session only.** The suite
  proves a tenant session lists only its own project's External IPs. It does not
  log in as a second tenant to observe a denial response, and the provider admin
  console cannot stand in for that: its Networking navigation group is empty, so
  a provider-admin session has no tenant External IPs page at all (verified on a
  deployed caas environment, 2026-11-19).
