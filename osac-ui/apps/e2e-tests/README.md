# Persisted OSAC UI E2E tests

This package contains committed Playwright tests for the OSAC UI against a
real, already-deployed environment. The browser harness and Playwright
projects live in [`apps/playwright`](../playwright); shared page helpers,
locators, and CLI helpers live in `libs/`.

The tests under `src/` are UI journeys, not unit tests. Use the app's Vitest
tests for isolated components. Use this package when the test must prove a real
browser flow through the deployed UI, authentication, proxy, and fulfillment
service. The separate `helper-tests/` directory checks the shared helpers in an
isolated browser with simulated responses.

## Directory structure

```text
apps/e2e-tests/
├── src/
│   ├── generic/                tenant, infrastructure, and catalog administration tests
│   ├── networking/             External IP flows and networking test data
│   ├── shared/                 resource configuration and run state used by both suites
│   └── vmaas/                  VM creation, deletion, resize, and power actions
├── helper-tests/               isolated helper regression tests
├── run_scripts/
│   ├── prepare-setup-for-ui-run.sh  install and validate prerequisites
│   └── run-ui-e2e.sh                run the configured VMaaS flow tags
├── extravars.yaml               VMaaS flow configuration
├── package.json                 package-local test and typecheck commands
├── playwright.helpers.config.ts isolated helper test configuration
└── tsconfig.json                TypeScript configuration

apps/playwright/
└── playwright.config.ts        browser projects and test discovery

libs/
├── locators/                   stable page locators
├── test-utils/                 login, page actions, waits, assertions, progress
└── osac-cli/                   OSAC CLI helpers used by UI flows
```

Add platform-wide UI tests under `src/generic/`, External IP flows under
`src/networking/` with their own test data, and VM-specific journeys under
`src/vmaas/`. The VMaaS full flow runs the generic setup and cleanup specs
through Playwright project dependencies and teardowns. Committed tests belong
here. The gitignored `apps/playwright/scratch/` directory is for throwaway
manual verification.

Use one `.spec.ts` file per focused journey. Keep reusable flow code beside
the feature when it is feature-specific; move genuinely shared behavior to
`libs/test-utils`. Use locators from `libs/locators` rather than duplicating
CSS or DOM selectors in a test.

## Adding a non-VMaaS test

Register a project with a focused spec pattern in `apps/playwright/playwright.config.ts`:

```ts
{
  name: 'projects',
  testDir: genericDir,
  testMatch: /projects\.spec\.ts$/,
  use: { ...devices['Desktop Chrome'], storageState: AUTH_FILE },
  dependencies: ['setup'],
}
```

Use the existing `setup` project and `AUTH_FILE` when all tests can use the
admin session. If a journey changes identity, logs out, or needs a tenant
user, log in explicitly with `openAndLogin` and disable tracing/video for the
project if credentials are entered. For a multi-step flow with dependencies,
make ordering explicit with Playwright `dependencies`, `teardown`, and, when
needed, `fullyParallel: false` or `workers: 1`.

A typical test has this shape:

```ts
import { expect, test } from '@playwright/test';

import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { openMainPageOption } from '../../../../libs/test-utils/src/main.page';

test('creates and displays a project', async ({ page }) => {
  await openAndLogin(
    page,
    '/',
    process.env.OSAC_ADMIN_USERNAME ?? '',
    process.env.OSAC_ADMIN_PASSWORD ?? '',
    'admin',
  );

  await openMainPageOption(page, 'virtualNetworks');
  // Use accessible roles/names and shared locators for the actual flow.
  await expect(page.getByRole('heading', { name: 'Virtual networks' })).toBeVisible();
});
```

The page and action names in the example are illustrative. Follow the
nearest existing page helper and locator for the feature being tested.

## Rules for reliable flows

- Use accessible roles, labels, and shared locators. Avoid brittle CSS and
  `data-testid` unless the project explicitly requires it.
- Let Playwright assertions and helper wait functions observe state. Do not
  add fixed sleeps for page loads, reconciliation, or provisioning.
- Give created resources unique, run-specific names. Never depend on an
  object left by another test or on a pre-existing shared resource.
- Clean up in the same flow, using `try/finally`, `afterEach`, `afterAll`, or
  a Playwright teardown project. Delete in reverse dependency order and wait
  for deletion to complete.
- Keep tenant operations in the correct tenant identity and preserve tenant
  isolation. Do not use admin credentials to prove a tenant-only behavior.
- Keep passwords, tokens, cookies, and generated credentials out of source,
  logs, screenshots, traces, and videos. The setup project already disables
  trace/video while entering the real password.
- Keep feature-specific data with the tests that use it. `extravars.yaml`
  supplies the existing generic resource projects and the VMaaS flows.

## External IP (networking) suite

`src/networking/` covers the tenant External IP management UI: list columns,
create wizard validation, create and delete, tenant scoping, cross-tenant denial
(when a second tenant admin is configured), and the list error state. Its inputs
are environment variables, not `extravars.yaml`, so it can never pick up a VMaaS
value by accident.

| Variable                                                              | Required          | Description                                                                       |
| --------------------------------------------------------------------- | ----------------- | --------------------------------------------------------------------------------- |
| `OSAC_UI_BASE_URL`                                                    | yes               | URL of the deployed `osac-ui` instance                                            |
| `OSAC_ADMIN_USERNAME` / `OSAC_ADMIN_PASSWORD`                         | yes               | Provider admin used for the External IP pool                                      |
| `OSAC_E2E_TENANT_NAME`                                                | yes\*             | Tenant created by the VMaaS tenant flow, used to read its break-glass credentials |
| `OSAC_TENANT_ADMIN_USERNAME` / `OSAC_TENANT_ADMIN_PASSWORD`           | alternative to \* | Tenant-admin credentials supplied directly                                        |
| `OSAC_EXT_IP_CIDR`                                                    | one of            | CIDR the suite uses to create and later delete its own `/24` or larger pool       |
| `OSAC_EXT_IP_POOL`                                                    | one of            | Name of an existing pool the suite reuses instead of creating one                 |
| `OSAC_EXT_IP_PROJECT`                                                 | no                | Project for the created External IP; empty uses the tenant default project        |
| `OSAC_E2E_SECOND_TENANT_USERNAME` / `OSAC_E2E_SECOND_TENANT_PASSWORD` | no                | Second tenant's admin; enables the cross-tenant denial assertion                  |
| `OSAC_E2E_INSTANCE`                                                   | no                | Run index appended to resource names; defaults to `1`                             |

Pick the pool CIDR from an address range the target cluster can actually
allocate from — the pytest networking suite allocates `172.27.<n>.0/24`
subnets for the same purpose. If allocation cannot succeed, the flow fails with
the observed row text instead of a bare timeout.

**The deployment must enable provider networking.** With
`global.networking.provisioningEnabled: false` the fulfillment service rejects
every External IP create with `FailedPrecondition: provider networking is
disabled` (`private_external_ips_server.go`), so only the list and error-state
checks can pass. The stock `osac-installer/values/dev/kind-instance.yaml` dev
profile sets `false`, so a default kind instance is **not** enough for this
suite; point it at an installation with provider networking enabled. See
[Installing with provider networking](../../../docs/guides/installation/customer-install-guide.md#44-installing-with-provider-networking).

**On a fresh install, run the phases separately.** Tenant onboarding waits for a
`READY` pool (its `default-nat` address comes from one), so the pool must exist
first; and the pool project refuses to recreate a pool that already exists, so
the flows must reuse it afterwards.

1. Create the pool once, before the first tenant. It needs a CIDR the cluster
   can allocate from, and this project has no teardown, so the pool stays:

   ```bash
   export OSAC_EXT_IP_CIDR=172.27.9.0/24
   pnpm --filter @osac/playwright exec playwright test --project=external-ip-pool
   ```

2. Onboard the tenant in create mode (`run-ui-e2e.sh --tag=create_tenant`, or
   `OSAC_VM_FLOW_MODE=create pnpm ... --project=tenant`): the `tenant` project
   defaults to `full`, whose teardown deletes the tenant and the RoleBinding that
   hold the credentials this suite reads. A tenant onboarded without a pool
   reports `DefaultNetworkingReady=False`.

3. Reuse the pool for the flows and leave the CIDR unset, so the pool project
   skips itself instead of failing its "must not exist" check:

   ```bash
   unset OSAC_EXT_IP_CIDR
   export OSAC_EXT_IP_POOL=<pool-name>
   pnpm --filter @osac/playwright exec playwright test --project=external-ip --reporter=line
   ```

Run the whole suite. `external-ip` depends on `external-ip-pool`, and its
teardown (`external-ip-delete`) removes the External IP and, when the suite
created it, the pool:

```bash
cd osac-ui
export OSAC_UI_BASE_URL=https://osac-ui-<namespace>.<cluster-domain>
export OSAC_ADMIN_USERNAME=<keycloak-admin>
export OSAC_E2E_TENANT_NAME=<tenant-name>
export OSAC_EXT_IP_POOL=<pool-name>
read -rs OSAC_ADMIN_PASSWORD && export OSAC_ADMIN_PASSWORD

pnpm --filter @osac/playwright exec playwright test \
  --project=external-ip --reporter=line
```

To let a suite-owned pool be created instead, export `OSAC_EXT_IP_CIDR` and leave
`OSAC_EXT_IP_POOL` unset; see the phase-1 command above.

Playwright runs `external-ip-pool`, then `external-ip` (`create`, `isolation`,
`list` in that order), then `external-ip-delete`, which deletes the External IP
as the tenant and, when the suite owns the pool, the pool as the provider admin.
Pool cleanup happens only when the suite owns it: `OSAC_EXT_IP_CIDR` is set and
`OSAC_EXT_IP_POOL` is not. Otherwise the teardown logs why it leaves the pool
alone.

Cleanup never deletes by name alone. The create specs record the ids of the
resources they created in `/tmp/osac-external-ip-state-<OSAC_E2E_INSTANCE>.json`,
and `external-ip-delete` deletes only records whose id still matches. Without a
record it refuses to act; a resource that happens to use the configured name is
left in place.

Run one suite run per instance index. A create drops the instance's previous
record, so clean up an interrupted run first (`external-ip-delete --no-deps`)
before starting another run with the same `OSAC_E2E_INSTANCE`.

Only delete a leftover through the UI when you can prove the run created it, for
example because the live id matches the recorded id or you watched that run
create it. If ownership cannot be verified, leave the resource in place.

If the suite created the pool and a tenant was onboarded afterwards, that
tenant's `default-nat` address comes from the same pool. The provider refuses to
delete a pool that still has allocated addresses, so the teardown reports the
pool as **retained** with the reason instead of failing the run. Release the
allocated addresses first (delete the External IPs that reference the pool; a
tenant's `default-nat` goes away with its tenant), then delete the pool — the
provider rejects a pool deletion while any allocation remains.

Run the pool alone, or keep the resources for inspection:

```bash
# Provision the pool only; no teardown runs, so it stays for a later run.
pnpm --filter @osac/playwright exec playwright test --project=external-ip-pool

# Delete only, against resources a previous run created. Re-export the same
# OSAC_E2E_INSTANCE the original run used, or these target another run's names.
export OSAC_E2E_INSTANCE=1
pnpm --filter @osac/playwright exec playwright test --project=external-ip-delete

# Cleanup after an interrupted run: external-ip-delete removes the External IP
# and, when the suite owns it, the pool. --no-deps keeps it from pulling in the
# create flows; it works with an empty list too.
pnpm --filter @osac/playwright exec playwright test \
  --project=external-ip-delete --no-deps

# Discovery without a browser run.
pnpm --filter @osac/playwright exec playwright test \
  --project=external-ip --list
```

Iterating on one spec: `--project=external-ip` pulls in the pool project as a
dependency and the delete project as a teardown, so a single-file run still
touches three projects. Add `--no-deps` to run only that file. The list and
error specs stand alone; the create, isolation and delete specs need the
resource they act on:

```bash
# Read-only checks, no dependency or teardown projects. The column test needs
# at least one visible External IP row; on an empty deployment run the two
# tests that need no row instead:
pnpm --filter @osac/playwright exec playwright test list.external_ip.spec.ts \
  --project=external-ip --no-deps --grep "reports a service error" --headed --reporter=line
pnpm --filter @osac/playwright exec playwright test list.external_ip.spec.ts \
  --project=external-ip --no-deps --grep "refuses to report an External IP as absent" \
  --reporter=line
```

Reusing an existing pool instead of provisioning one:

```bash
export OSAC_EXT_IP_POOL=<existing-pool-name>
pnpm --filter @osac/playwright exec playwright test --project=external-ip
```

Both pool projects then skip, and the teardown leaves the pool in place.

## Prepare and run

To run the isolated helper tests without a cluster, install Playwright's
Chromium browser and run from `osac-ui/`:

```bash
pnpm --filter @osac/e2e-tests exec playwright install chromium
pnpm --filter @osac/e2e-tests run test:browser
```

These tests do not replace the deployed UI journeys below.

Run from the `osac-ui/` workspace root:

```bash
pnpm install
pnpm --filter @osac/playwright exec playwright install chromium

export OSAC_UI_BASE_URL=https://osac-ui.example.test
export OSAC_ADMIN_USERNAME=<keycloak-test-user>
read -rs OSAC_ADMIN_PASSWORD && export OSAC_ADMIN_PASSWORD

# Discover the full VMaaS suite and its generic prerequisite and cleanup tests.
pnpm --filter @osac/playwright exec playwright test \
  --project=vmaas-all-full-flows --list

# Discover all generic resource tests through their existing projects.
pnpm --filter @osac/playwright exec playwright test \
  --project=tenant --project=disk-image-lifecycle \
  --project=instance-type-lifecycle --project=storage-tier --project=catalog --list

# Typecheck the persisted E2E package.
pnpm --filter @osac/e2e-tests run typecheck
```

Use `run-ui-e2e.sh --tag=vmaas_all` for the full VMaaS suite or select the
generic resource flows with `full_tenant`, `full_disk_image`,
`full_instance_type`, and `full_storage-tier`. See the
[browser harness setup](../playwright/README.md#setup) for live credentials and
CLI setup. To exercise disk image or instance type state transitions, select
`--project=disk-image-lifecycle` or `--project=instance-type-lifecycle`.

Before submitting UI changes, also run the workspace checks required by
[`osac-ui/AGENTS.md`](../../AGENTS.md):

```bash
pnpm run typecheck
pnpm lint
pnpm test
```

## Temporary VMaaS tenant setup workaround

On deployments affected by [OSAC-7988](https://redhat.atlassian.net/browse/OSAC-7988)
and [OSAC-7989](https://redhat.atlassian.net/browse/OSAC-7989), set
`OSAC_E2E_TENANT_BOOTSTRAP_CONFIG` to the directory of an authenticated private
OSAC CLI session for the same deployment. The private session must be allowed
to read the new test tenant's break-glass Secret. The other steps continue to
use the ordinary browser and CLI test identities.

```bash
OSAC_E2E_TENANT_BOOTSTRAP_CONFIG="$private_osac_cli_config" OSAC_VM_FLOW_MODE=full \
  pnpm --filter @osac/playwright exec playwright test \
  --project=vmaas-all-full-flows --workers=1 --reporter=line
```

This skips the `shared` tenant status check and the credentials dialog
assertions. The flow creates a fresh tenant, verifies that both `system` and
the new tenant are `Synced`, and uses the exact break-glass Secret ID returned
by the browser's deployment. Teardown uses the recorded tenant identity to
clean up a tenant created by this run if credential setup fails.
