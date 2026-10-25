# @osac/playwright

Playwright browser tests for manually verifying `osac-ui` against a **live,
already-deployed** cluster — the running proxy, SPA, Keycloak realm, and
`fulfillment-service` backend all in one real environment.

This is not a CI suite. There is no mock server for `fulfillment-service`
and no way to run this hermetically — it exists for AI agents to manually confirm a
change actually works end-to-end against a real deployment, the way you'd
otherwise do by hand in a browser.

## Prerequisites

- A reachable `osac-ui` deployment (any HTTPS URL that serves it).
- A Keycloak user on that deployment's realm to log in as. This package
  doesn't provision one — use an existing test account, or create one on the
  target environment first.
- Playwright's browser binaries installed once per machine (see Setup below).

## Setup

Run these commands from the `osac-ui/` workspace root. In this monorepo, that
is the `osac-ui/` directory.

```bash
pnpm install
pnpm --filter @osac/playwright exec playwright install chromium
```

## Running

Required environment variables:

| Variable           | Description                                                                                       |
| ------------------ | ------------------------------------------------------------------------------------------------- |
| `OSAC_UI_BASE_URL` | Full URL of the `osac-ui` deployment to test, e.g. `https://osac-ui-<namespace>.<cluster-domain>` |
| `OSAC_USERNAME`    | Username of a Keycloak test user on that deployment's realm                                       |
| `OSAC_PASSWORD`    | Password for that user                                                                            |

```bash
export OSAC_UI_BASE_URL=https://... OSAC_USERNAME=...
read -rs OSAC_PASSWORD && export OSAC_PASSWORD   # avoids leaving the password in shell history
pnpm playwright
```

(`pnpm playwright` is a workspace-root shortcut for `pnpm --filter @osac/playwright
run playwright`.) None of these have defaults — each is required, so a missing
value fails immediately with a clear error instead of silently pointing at the
wrong environment. `pnpm playwright` logs in, runs the [smoke
test](#smoke-test), and runs every spec under `scratch/` — see [Writing a
test](#writing-a-test). Tracked UI flows are excluded from this default
command; select a project explicitly in the [flow instructions](#tenant-flow-tests).

For iterating on a `scratch/` spec, logging in on every run is slow and adds a
real Keycloak request each time. Log in once, then re-run just the spec as
many times as you need, reusing the saved session:

```bash
pnpm playwright:setup   # logs in once, writes .auth/user.json
pnpm playwright:run     # runs scratch/ specs only, no login
pnpm playwright:run     # ...repeat as many times as you're iterating
```

`pnpm playwright:run` never re-authenticates — if `.auth/user.json` is missing
or the session has expired, re-run `pnpm playwright:setup` first.
It needs at least one spec under `apps/playwright/scratch/`; if that directory
has no specs, Playwright reports that no tests were found.

### Tenant flow tests

The tenant specs read `tenant_name` from `apps/e2e-tests/extravars.yaml`.
Before running, set it to a tenant that does not already exist. The tenant
create flow requires that the configured tenant does not already exist; the
delete flow requires that it exists. All VMaaS specs skip before browser
actions when `tenant_name` is unset or empty.

Set the deployment and login variables from the [Running](#running) section,
then run the flows:

```bash
export OSAC_UI_BASE_URL=https://osac-ui-<namespace>.<cluster-domain>
export OSAC_USERNAME=<keycloak-user>
read -rs OSAC_PASSWORD && export OSAC_PASSWORD
```

Run the tenant create and delete flows together. The `vmaas-delete` project
depends on `vmaas`, so creation runs before deletion:

```bash
pnpm --filter @osac/playwright exec playwright test --project=vmaas-delete
```

Run the tenant create and delete flows separately:

```bash
pnpm --filter @osac/playwright exec playwright test \
  create.tenant.spec.ts --project=vmaas

pnpm --filter @osac/playwright exec playwright test \
  delete.tenant.spec.ts --project=vmaas-delete --no-deps
```

### Storage tier flow tests

The Storage tier specs read `storage_tier.name`, `description`, `backend`, and
`protocol` from `apps/e2e-tests/extravars.yaml`. The configured backend must
already exist. The create test checks that it is Ready and uses the `lvms`
provider on Storage Backends, then verifies that it is selectable in the create
form. Each run appends a unique suffix to `storage_tier.name`. Set
`OSAC_STORAGE_TIER_TEST_RUN_ID` to a stable 1–32 character alphanumeric value
when you need the same suffix across separate commands.

The project run has two steps:

1. `vmaas-storage-tier` creates the Storage tier and verifies its configuration.
2. `vmaas-storage-tier-delete` deletes the configured Storage tier after the
   create flow completes.

Set the common variables from the [Running](#running) section, then run the
complete flow from the repository root:

```bash
pnpm --filter @osac/playwright exec playwright test --project=vmaas-storage-tier
```

Playwright runs the create project followed by its teardown. Add `--headed` if
a graphical display is available:

```bash
pnpm --filter @osac/playwright exec playwright test \
  --project=vmaas-storage-tier --headed
```

To run Storage tier create and delete separately, export the same run ID before
both commands. The specs check `storage_tier.name` and skip if it is unset;
they do not use `tenant_name` or create or delete a tenant:

```bash
export OSAC_STORAGE_TIER_TEST_RUN_ID=mytest123

pnpm --filter @osac/playwright exec playwright test \
  create.storage_tier.spec.ts --project=vmaas-storage-tier --no-deps

pnpm --filter @osac/playwright exec playwright test \
  delete.storage_tier.spec.ts --project=vmaas-storage-tier-delete --no-deps
```

The `--no-deps` option skips the create project's automatic teardown, so the
explicit delete project runs after the separate create command. The delete
test creates the configured tier from `extravars.yaml` if it is absent, then
deletes the tier identified by that run ID. Use the same
`OSAC_STORAGE_TIER_TEST_RUN_ID` to target a tier from an interrupted run.

### Disk image flow tests

The disk image specs read their non-sensitive seed values from
`apps/e2e-tests/extravars.yaml`. Set `disk_image.name` to a unique name for
the target cluster. The specs skip if `disk_image.name` is unset. The create,
lifecycle, and delete flows use the configured disk image values and lifecycle
actions.

The disk image and instance type create projects own their delete projects as
teardown, so cleanup runs after their lifecycle projects even if a lifecycle
assertion fails.

Set the deployment and login variables from the [Running](#running) section,
then run the flow:

```bash
export OSAC_UI_BASE_URL=https://osac-ui-<namespace>.<cluster-domain>
export OSAC_USERNAME=<keycloak-user>
read -rs OSAC_PASSWORD && export OSAC_PASSWORD
```

Run the disk image lifecycle flow:

```bash
pnpm --filter @osac/playwright exec playwright test \
  --project=vmaas-disk-image-lifecycle
```

The `vmaas-disk-image-lifecycle` project depends on `vmaas-disk-image`. The
create project's teardown deletes the image after the lifecycle project
finishes, including when the lifecycle test fails. To run only the delete
project, use `--project=vmaas-disk-image-delete --no-deps`. If the configured
image is absent, the delete test creates it from `extravars.yaml` and then
deletes it.
Each run appends a unique suffix to `disk_image.name`, and the full name appears
in the test title. To clean up an interrupted run, set
`OSAC_DISK_IMAGE_TEST_RUN_ID` to that run's suffix when invoking the delete
project.

### Instance type flow tests

The instance type tests read their hard-coded test data from
`apps/e2e-tests/extravars.yaml`. Change the `instance_type` values there before
running the flow if you need a different name, description, vCPU/memory values,
or GPU configuration. The specs skip if `instance_type.name` is unset. The
create test fails clearly if that instance type already exists.

The tests run in this dependency order:

1. `vmaas-instance-type` creates the instance type and verifies it is `Active`.
2. `vmaas-instance-type-lifecycle` verifies the `Deprecate`, `Obsolete`, and
   `Reactivate` transitions.
3. `vmaas-instance-type-delete` deletes the instance type and waits for its row
   to disappear.

Set the common variables from the [Running](#running) section, then run the
complete instance type flow from the `osac-ui/` workspace root:

```bash
pnpm --filter @osac/playwright exec playwright test \
  --project=vmaas-instance-type-lifecycle
```

Playwright runs the create dependency, then runs the create project's teardown
to delete the instance type after the lifecycle test. Add `--headed` to the
command when a graphical display is available:

```bash
pnpm --filter @osac/playwright exec playwright test \
  --project=vmaas-instance-type-lifecycle --headed
```

To run the three instance-type tests separately, run them in this order:

```bash
pnpm --filter @osac/playwright exec playwright test \
  create.instance_type.spec.ts --project=vmaas-instance-type --no-deps

pnpm --filter @osac/playwright exec playwright test \
  lifecycle.instance_type.spec.ts --project=vmaas-instance-type-lifecycle --no-deps

pnpm --filter @osac/playwright exec playwright test \
  delete.instance_type.spec.ts --project=vmaas-instance-type-delete --no-deps
```

The `--no-deps` option is appropriate only for the separate commands because
the preceding command has already established the required instance type.
Running the delete test removes the instance type from the deployment. If the
requested instance type is absent, the standalone delete test creates it from
`extravars.yaml` first; unrelated instance types already listed in the table
are not modified.

To run the Storage tier and instance type flows together, include both terminal
projects; each project runs its own resource lifecycle:

```bash
pnpm --filter @osac/playwright exec playwright test \
  --project=vmaas-storage-tier --project=vmaas-instance-type-lifecycle
```

### Catalog CLI flow tests

The catalog flow runs the `osac` CLI from Playwright, then opens the live UI,
logs in, and verifies the created item on the Catalog page. Authenticate the
CLI against the target environment before starting the test, for example with
`osac login`. Set `OSAC_UI_BASE_URL`, `OSAC_USERNAME`, and `OSAC_PASSWORD` for
the browser verification.

The catalog configuration is in `apps/e2e-tests/extravars.yaml`:

| Parameter                                 | Description                                                       |
| ----------------------------------------- | ----------------------------------------------------------------- |
| `catalog.name`                            | Unique catalog item name to create and delete                     |
| `catalog.template`                        | Existing compute instance template referenced by the catalog item |
| `catalog.title`                           | Catalog title                                                     |
| `catalog.description`                     | Catalog description                                               |
| `catalog.fields.instance_type.name`       | Existing instance type referenced by the catalog item             |
| `catalog.fields.instance_type.vcpus`      | Expected vCPU value displayed for the referenced instance type    |
| `catalog.fields.instance_type.memory_gib` | Expected memory value displayed for the referenced instance type  |
| `catalog.fields.boot_disk.size_gib`       | Boot-disk storage value displayed for the catalog item            |
| `catalog.fields.disk_image.name`          | Existing disk image referenced by the catalog item                |

Optional variables:

| Variable          | Description                                                                             |
| ----------------- | --------------------------------------------------------------------------------------- |
| `OSAC_CLI_PATH`   | Path to the `osac` executable; defaults to `osac` from `PATH`                           |
| `OSAC_CLI_CONFIG` | CLI configuration directory; when unset, the CLI uses its normal configuration location |

Run creation and deletion together. The delete project depends on the create
project, and each CLI operation is followed by a JSON lookup and a browser
check of the Catalog page:

```bash
export OSAC_UI_BASE_URL=https://osac-ui-<namespace>.<cluster-domain>
export OSAC_CLI_CONFIG="$HOME/.config/osac"

pnpm --filter @osac/playwright exec playwright test \
  --project=vmaas-catalog-delete
```

Run the flows separately:

```bash
pnpm --filter @osac/playwright exec playwright test \
  create.catalog.spec.ts --project=vmaas-catalog

pnpm --filter @osac/playwright exec playwright test \
  delete.catalog.spec.ts --project=vmaas-catalog-delete --no-deps
```

Use `--headed` when a graphical display is available. Run these commands from
the `osac-ui/` workspace root; pass only the spec filename because the
Playwright configuration already defines the VMaaS spec directory.

Optional:

| Variable              | Description                                                                                                                                                                                                                                                                                       |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `IGNORE_HTTPS_ERRORS` | Set to `true` to trust self-signed/cluster-internal CA certs, common on dev and lab clusters. Off by default because this flow submits a real Keycloak password — it disables TLS verification for the whole browser context, not just `localhost`, so only enable it against clusters you trust. |

Testing against a local `pnpm dev` instance instead of a remote deployment:
requires `pnpm dev` running in another terminal, and still needs
`OSAC_USERNAME`/`OSAC_PASSWORD`.

```bash
export OSAC_UI_BASE_URL=http://localhost:5173 OSAC_USERNAME=...
read -rs OSAC_PASSWORD && export OSAC_PASSWORD
pnpm playwright
```

The backing `fulfillment-service`/Keycloak for a local dev server is often a
dev/lab cluster with a self-signed or cluster-internal CA cert — the real
login redirect goes there even though the UI itself is on `localhost`, so if
you hit a TLS error, explicitly opt in with `IGNORE_HTTPS_ERRORS=true` rather
than trusting it by default:

```bash
export OSAC_UI_BASE_URL=http://localhost:5173 OSAC_USERNAME=... IGNORE_HTTPS_ERRORS=true
read -rs OSAC_PASSWORD && export OSAC_PASSWORD
pnpm playwright
```

## How authentication works

`osac-ui` has no test-mode auth bypass — every login goes through a real
Keycloak realm via the app's normal OIDC flow. Rather than repeating that
flow (and Keycloak's own login page) for every test, this harness follows
Playwright's standard pattern for testing authenticated apps:

1. A `setup` project (`src/auth.setup.ts`) opens the app, which auto-redirects
   to Keycloak when unauthenticated, fills in `OSAC_USERNAME`/`OSAC_PASSWORD`
   on Keycloak's real login form, and saves the resulting browser session
   (cookies) to `.auth/user.json`.
2. Every real test (the `smoke` and `chromium` projects) declares a dependency
   on `setup` and loads that saved session before the test body runs — so
   tests start already logged in.

`pnpm playwright` (and `pnpm playwright:setup`) always re-run `setup` first,
regenerating `.auth/user.json` fresh. `pnpm playwright:run` skips `setup`
entirely and reuses whatever session is already on disk — see
[Running](#running).

`.auth/user.json` contains a live, real session (the actual `osac-access`
cookie) — it's gitignored and must never be committed or shared, and
`auth.setup.ts` restricts it to `0600` (and its directory to `0700`) so other
local accounts on a shared machine can't read it.

If the target realm's login page uses a custom Keycloak theme, the field/
button selectors in `auth.setup.ts` (written against Keycloak's default
theme) may need adjusting.

## Smoke Test

`src/smoke.spec.ts` is the one persisted, committed spec in this package — a
harness self-check, not a feature test. It logs in (via `setup`) and asserts
that the app actually loads and renders its masthead. Its job is to answer
"is the harness broken, or is my change broken?" before you spend time
writing a real `scratch/` spec: if the smoke test fails, the problem is
almost certainly auth, config, or environment — not the change you're trying
to verify.

It runs automatically as part of `pnpm playwright` (and standalone via `pnpm
--filter @osac/playwright exec playwright test --project=smoke`). Unlike
`scratch/` specs, do not delete or repurpose it, and do not add more specs
here — this package still isn't a test suite; one fixed sanity check is the
exception, not a precedent.

## Writing a test

**This is not a test suite.** Beyond the one smoke test above, this package
provides the harness (auth + config) only. Test specs are throwaway, written
ad hoc for a specific manual-verification task (typically by an AI agent
working through a change) and run once, then discarded.

Specs live under `scratch/` — a gitignored directory that only exists on your
machine, so nothing written there is ever committed or lands in a PR. Gitignore
only keeps it out of git, though — `pnpm playwright` re-runs every spec still
in the directory, so delete a spec once you're done with it. Do not write
specs under `src/` (that's reserved for the harness and the smoke test), and
do not treat anything under `scratch/` as regression coverage — this harness
has no CI and nothing under `scratch/` is ever committed.

```bash
mkdir -p apps/playwright/scratch
cat > apps/playwright/scratch/check.spec.ts <<'EOF'
import { expect, test } from '@playwright/test';

test('loads the authenticated dashboard', async ({ page }) => {
  await page.goto('/');

  await expect(page.getByRole('button', { name: 'Account menu' })).toBeVisible();
});
EOF
```

It runs in the `chromium` project — already authenticated via `auth.setup.ts`,
no need to handle login in the test itself. Run it with:

```bash
export OSAC_UI_BASE_URL=... OSAC_USERNAME=...
read -rs OSAC_PASSWORD && export OSAC_PASSWORD
pnpm playwright
```

`pnpm playwright` runs every spec under `scratch/` — delete the file when
you're done with it. Gitignore keeps it out of git either way, but it does
not stop Playwright from finding and re-running a stale spec next time.
