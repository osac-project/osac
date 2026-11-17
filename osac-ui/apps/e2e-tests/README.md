# Persisted OSAC UI E2E tests

This package contains committed Playwright tests for the OSAC UI against a
real, already-deployed environment. The browser harness and Playwright
projects live in [`apps/playwright`](../playwright); shared page helpers,
locators, and CLI helpers live in `libs/`.

The tests under `src/` are UI journeys, not unit tests. Use the app's Vitest
tests for isolated components. Use this package when the test must prove a real
browser flow through the deployed UI, authentication, proxy, and fulfillment
service. The separate `tests/` directory checks the shared helpers in an
isolated browser with simulated responses.

## Directory structure

```text
apps/e2e-tests/
├── src/
│   ├── vmaas/                  VMaaS flows and VMaaS test data
│   └── networking/             External IP flows and networking test data
├── tests/                      isolated helper regression tests
├── run_scripts/
│   ├── prepare-setup-for-ui-run.sh  install and validate prerequisites
│   └── run-ui-e2e.sh                run the configured VMaaS flow tags
├── extravars.yaml               VMaaS flow configuration
├── package.json                 package-local test and typecheck commands
├── playwright.unit.config.ts    isolated helper test configuration
└── tsconfig.json                TypeScript configuration

apps/playwright/
└── playwright.config.ts        browser projects and test discovery

libs/
├── locators/                   stable page locators
├── test-utils/                 login, page actions, waits, assertions, progress
└── osac-cli/                   OSAC CLI helpers used by UI flows
```

Two suites are committed under `src/`: `vmaas/` and `networking/`. For a further
non-VMaaS journey, add a feature directory directly under `src/`, for example
`src/projects/`, `src/tenants/`, or `src/clusters/`. Do not put unrelated tests in
an existing feature directory, and do not put committed tests in
`apps/playwright/scratch/`; `scratch/` is gitignored and reserved for throwaway
manual verification.

Use one `.spec.ts` file per focused journey. Keep reusable flow code beside
the feature when it is feature-specific; move genuinely shared behavior to
`libs/test-utils`. Use locators from `libs/locators` rather than duplicating
CSS or DOM selectors in a test.

## Adding a non-VMaaS test

Adding a file under `src/` is not enough for Playwright to run it. Register a
project in `apps/playwright/playwright.config.ts`:

```ts
const projectsDir = path.join(
  path.dirname(fileURLToPath(import.meta.url)),
  '../e2e-tests/src/projects',
);

{
  name: 'projects',
  testDir: projectsDir,
  testMatch: /\.spec\.ts/,
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
- Keep test data in the feature directory. `extravars.yaml` and the
  `run-ui-e2e.sh` flow tags are currently VMaaS-specific; a non-VMaaS suite
  should normally use its own safe environment variables or fixtures instead.

## External IP (networking) suite

`src/networking/` covers the tenant External IP management UI: list columns,
create wizard validation, create and delete, tenant scoping, and the list error
state. Its inputs are environment variables, not `extravars.yaml`, so it can
never pick up a VMaaS value by accident.

| Variable | Required | Description |
| --- | --- | --- |
| `OSAC_UI_BASE_URL` | yes | URL of the deployed `osac-ui` instance |
| `OSAC_ADMIN_USERNAME` / `OSAC_ADMIN_PASSWORD` | yes | Provider admin used for the External IP pool |
| `OSAC_E2E_TENANT_NAME` | yes* | Tenant created by the VMaaS tenant flow, used to read its break-glass credentials |
| `OSAC_TENANT_ADMIN_USERNAME` / `OSAC_TENANT_ADMIN_PASSWORD` | alternative to * | Tenant-admin credentials supplied directly |
| `OSAC_EXT_IP_CIDR` | one of | CIDR the suite uses to create and later delete its own `/24` or larger pool |
| `OSAC_EXT_IP_POOL` | one of | Name of an existing pool the suite reuses instead of creating one |
| `OSAC_EXT_IP_PROJECT` | no | Project for the created External IP; empty uses the tenant default project |
| `OSAC_E2E_INSTANCE` | no | Run index appended to resource names; defaults to `1` |

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
suite; point it at an installation with provider networking enabled.

Run the whole suite. `external-ip` depends on `external-ip-pool`, and its
teardown (`external-ip-delete`) removes the External IP and, when the suite
created it, the pool:

```bash
cd osac-ui
export OSAC_UI_BASE_URL=https://osac-ui-<namespace>.<cluster-domain>
export OSAC_ADMIN_USERNAME=<keycloak-admin>
export OSAC_E2E_TENANT_NAME=<tenant-name>
export OSAC_EXT_IP_CIDR=172.27.9.0/24
read -rs OSAC_ADMIN_PASSWORD && export OSAC_ADMIN_PASSWORD

pnpm --filter @osac/playwright exec playwright test \
  --project=external-ip --reporter=line
```

Playwright runs `external-ip-pool`, then `external-ip` (`create`, `isolation`,
`list` in that order), then `external-ip-delete`, which deletes the External IP
as the tenant and the pool as the provider admin.

Run the pool alone, or keep the resources for inspection:

```bash
# Provision the pool only; no teardown runs, so it stays for a later run.
pnpm --filter @osac/playwright exec playwright test --project=external-ip-pool

# Delete only, against resources a previous run created.
pnpm --filter @osac/playwright exec playwright test --project=external-ip-delete
pnpm --filter @osac/playwright exec playwright test --project=external-ip-pool-delete

# Discovery without a browser run.
pnpm --filter @osac/playwright exec playwright test \
  --project=external-ip --list
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

# Confirm discovery without running tests.
pnpm --filter @osac/playwright exec playwright test \
  --project=projects --list

# Run the new project against the live deployment.
pnpm --filter @osac/playwright exec playwright test \
  --project=projects --reporter=line

# Typecheck the persisted E2E package.
pnpm --filter @osac/e2e-tests run typecheck
```

The `prepare-setup-for-ui-run.sh` and `run-ui-e2e.sh` scripts are convenient
for the existing VMaaS flows and their prerequisite checks. For a new
non-VMaaS project, direct Playwright commands are clearer unless the runner is
deliberately extended with a new flow and its prerequisites.

Before submitting UI changes, also run the workspace checks required by
[`osac-ui/AGENTS.md`](../../AGENTS.md):

```bash
pnpm run typecheck
pnpm lint
pnpm test
```
