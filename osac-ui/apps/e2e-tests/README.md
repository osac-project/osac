# Persisted OSAC UI E2E tests

This package contains committed Playwright tests for the OSAC UI against a
real, already-deployed environment. The browser harness and Playwright
projects live in [`apps/playwright`](../playwright); shared page helpers,
locators, and CLI helpers live in `libs/`.

These are UI journeys, not unit tests. Use the app's Vitest tests for isolated
components. Use this package when the test must prove a real browser flow
through the deployed UI, authentication, proxy, and fulfillment service.

## Directory structure

```text
apps/e2e-tests/
├── src/
│   └── vmaas/                  current VMaaS flows and VMaaS test data
├── run_scripts/
│   ├── prepare-setup-for-ui-run.sh  install and validate prerequisites
│   └── run-ui-e2e.sh                run the configured VMaaS flow tags
├── extravars.yaml               VMaaS flow configuration
├── package.json                 package-local typecheck command
└── tsconfig.json                TypeScript configuration

apps/playwright/
└── playwright.config.ts        browser projects and test discovery

libs/
├── locators/                   stable page locators
├── test-utils/                 login, page actions, waits, assertions, progress
└── osac-cli/                   OSAC CLI helpers used by UI flows
```

The only committed suite currently under `src/` is `vmaas/`. For a new
non-VMaaS journey, add a feature directory directly under `src/`, for example
`src/projects/`, `src/tenants/`, or `src/networking/`. Do not put unrelated
tests in `src/vmaas/` and do not put committed tests in
`apps/playwright/scratch/`; `scratch/` is gitignored and reserved for
throwaway manual verification.

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

## Prepare and run

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
