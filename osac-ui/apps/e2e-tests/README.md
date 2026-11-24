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

Add platform-wide UI tests under `src/generic/` and VM-specific journeys under
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
