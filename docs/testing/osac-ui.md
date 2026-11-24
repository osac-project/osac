# osac-ui

[Shared testing policy](../INTEGRATION-TESTING.md) defines tiers, ownership and
evidence. Inspect test source for individual cases. Paths are relative to
`osac-ui/` unless marked as repository-root paths.

Touched-area requirements: [component guide](../../osac-ui/AGENTS.md#test).

## Test tiers

| Tier | Suite (by directory/role) | Exercises for real | Faked or omitted |
|---|---|---|---|
| Unit | Vitest suites in `apps/app-frontend/src` and `libs/ui-components/src` (`pnpm test`) | Components, hooks, and API wiring with an in-memory Connect transport | No browser, no deployed proxy, no fulfillment service |
| UI E2E (manual) | Playwright flows in `apps/e2e-tests` (`src/vmaas/`, `src/networking/`) driven by the harness in `apps/playwright` | Real browser through the deployed SPA, Go proxy, Keycloak OIDC login, and fulfillment-service: VMaaS resource and VM journeys, External IP pool and tenant allocation | Only the failure-path tests fake a response: they intercept the list request and return HTTP 500. Everything else runs against the deployment; the suite needs admin and tenant credentials, so it never runs in CI |

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
- **List-test prerequisites differ.** `list.external_ip.spec.ts` holds three
  tests: the column check needs at least one visible External IP row, while the
  service-error and delete-refusal tests need none. Select them directly when a
  deployment has no rows, for example
  `--project=external-ip --no-deps --grep "reports a service error"`; all three
  need the tenant admin credentials.
- **Cleanup is ownership-scoped.** The create specs record the created ids in
  `/tmp/osac-external-ip-state-<OSAC_E2E_INSTANCE>.json`, and `external-ip-delete`
  deletes only records whose id still matches. Without a record it refuses to
  act, so a name collision cannot remove another run's External IP or pool. Run
  one suite run per instance index, and clean up an interrupted run before
  starting another one.
- **A suite-created pool can outlive the run.** When the tenant is onboarded
  after the pool, its `default-nat` address comes from that pool, and the
  provider refuses to delete a pool with allocated addresses. The teardown then
  logs the retained pool and its reason instead of failing. Pool cleanup is part
  of `external-ip-delete`, so a mixed or unfiltered run removes the pool in that
  teardown, after the External IP. After an interrupted self-provisioned run,
  restore the original `OSAC_E2E_INSTANCE` and `OSAC_EXT_IP_CIDR`, unset
  `OSAC_EXT_IP_POOL`, and then run `external-ip-delete --no-deps`.
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
- **Attach and detach are out of scope for OSAC-6539.** The product supports
  attaching an External IP from a VM or Cluster details page, which moves the row
  to `In use` and disables Delete with "Detach this external IP before deleting
  it." That is deliberately not covered here: the story asks for list, create,
  delete, isolation, immutability and error states. Exercising it means driving
  the VM or Cluster details journey, so it belongs to those suites or a follow-up
  ticket; none exists yet.
- **Project filter and project scoping are not exercised.** The isolation spec
  never selects the filter or checks that listed rows belong to an allowed
  project, and only asserts the created row's Project cell when
  `OSAC_EXT_IP_PROJECT` is set.
- **Cross-tenant denial needs a second tenant identity.** When
  `OSAC_E2E_SECOND_TENANT_USERNAME` and `OSAC_E2E_SECOND_TENANT_PASSWORD` name a
  second tenant's admin, the suite logs in as that tenant and asserts the first
  tenant's External IP is not visible, then skips that step with a logged reason
  when the variables are unset. The provider admin console cannot substitute for
  it: its Networking navigation group is empty, so a provider-admin session has
  no tenant External IPs page at all (verified on a deployed caas environment,
  2026-11-19).
