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
- For a standalone VM launch run, a tenant-scoped SSH public-key Secret must
  exist in the tenant's Default project. The VM wizard filters for
  `SECRET_TYPE_SSH_PUBLIC_KEY` with a `public_key` data entry; without one it
  shows `No secret available` and the SSH-key step cannot complete. The full
  VMaaS flow creates and removes this Secret automatically. For a standalone
  run, create one with the private OSAC CLI using a non-sensitive public key
  file:

  ```bash
  osac --tenant <tenant-name> create secret \
    --name <ssh-secret-name> --type ssh-public-key \
    --from-file=public_key=<path-to-public-key>
  ```

- Playwright's browser binaries installed once per machine (see Setup below).

## Setup

Run these commands from the `osac-ui/` workspace root. In this monorepo, that
is the `osac-ui/` directory.

```bash
pnpm install
pnpm --filter @osac/playwright exec playwright install chromium
```

For a fresh RHEL 9 host, use the two-stage helper instead. Preparation does
not read credentials, select a flow, or run tests; the second command does.
The preparation step also validates the OSAC CLI and, when a privileged
kubeconfig is available, checks that the configured VM can be scheduled before
the browser flow starts.

```bash
bash apps/e2e-tests/run_scripts/prepare-setup-for-ui-run.sh \
  --osac-ui-dir="$PWD" --kubeconfig="${KUBECONFIG:-}"

bash apps/e2e-tests/run_scripts/run-ui-e2e.sh \
  --osac-ui-dir="$PWD" --tag=vmaas_all \
  --instance=1 --admin-user=<keycloak-user> \
  --admin-password-file=/path/to/password
```

The default HTML reporter does not show the full live test log in the terminal.
Add `--reporter=line` to the runner command for live progress and test output.

The preparation helper installs Node.js, npm, pnpm, Playwright dependencies,
and Chromium. It installs `openshift-clients` when possible, validates the
OSAC CLI, and validates the checkout's `extravars.yaml`. When `oc` and a
reachable `KUBECONFIG` are available, it checks both OSAC routes and performs a
VMaaS capacity preflight. A capacity failure means the cluster needs more
allocatable memory/CPU or the configured instance type must be reduced; the
script never changes cluster sizing automatically. The runner accepts the flow
tags listed below, validates the deployment and VMaaS prerequisites, and keeps
run-time credentials outside the setup step. Pass the admin credentials with
`--admin-user=USER` and `--admin-password=PASSWORD`; the username defaults to
`admin`. For safer handling, use `--admin-password-file=PATH` instead of
putting the password in shell history. A missing password stops the run with a
fix instruction. For VMaaS flows, the runner also authenticates the OSAC CLI
for the duration of the run using the same credentials; temporary CLI files
are removed on exit. `--instance=INDEX` defaults to `1` and appends the index
to every VMaaS resource name, allowing isolated runs such as
`tenant-item-e2e-1` and `tenant-item-e2e-2`.

If the CLI is not already installed, provide a stable fulfillment-service
version to download it, or pass an existing binary:

```bash
bash apps/e2e-tests/run_scripts/prepare-setup-for-ui-run.sh \
  --osac-ui-dir="$PWD" --osac-cli-version=<version>
# or: --osac-cli=/path/to/osac
```

## Running

Environment variables:

| Variable              | Description                                                                                       |
| --------------------- | ------------------------------------------------------------------------------------------------- |
| `OSAC_UI_BASE_URL`    | Full URL of the `osac-ui` deployment to test, e.g. `https://osac-ui-<namespace>.<cluster-domain>` |
| `OSAC_API_BASE_URL`   | Optional Fulfillment API URL; auto-discovered from the `fulfillment-api` route when possible      |
| `OSAC_CLI_PATH`       | Optional path to the `osac` binary; the runner also checks `~/.local/bin/osac`                    |
| `OSAC_E2E_INSTANCE`   | Optional resource-name index for direct Playwright commands; defaults to `1`                      |
| `OSAC_ADMIN_USERNAME` | Username of the Keycloak admin test user on that deployment's realm                               |
| `OSAC_ADMIN_PASSWORD` | Password for the admin test user                                                                  |

When `OSAC_UI_BASE_URL` is unset, the runner reads the `osac-ui` route from
the standard kubeconfig locations or derives the standard route from the
kubeconfig API server. When `OSAC_API_BASE_URL` is unset, it reads the
`fulfillment-api` route or derives it from a standard `osac-ui-...` route name.
Set either URL explicitly for deployments that use different routes.

```bash
export OSAC_UI_BASE_URL=https://... OSAC_ADMIN_USERNAME=...
read -rs OSAC_ADMIN_PASSWORD && export OSAC_ADMIN_PASSWORD   # avoids leaving the password in shell history
pnpm playwright
```

(`pnpm playwright` is a workspace-root shortcut for `pnpm --filter @osac/playwright
run playwright`.) `OSAC_UI_BASE_URL`, `OSAC_ADMIN_USERNAME`, and `OSAC_ADMIN_PASSWORD` are
required and fail immediately when missing instead of silently pointing at the
wrong environment. The harness always logs in as the admin test user. `pnpm
playwright` logs in, runs the [smoke
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

The tenant specs read `tenant_name` and the optional break-glass replacement
password from `apps/e2e-tests/extravars.yaml`.
Set `tenant_password` to the password used after the generated break-glass user
completes its first login. When it is omitted, the tenant name is used. The
create flow logs in with the generated break-glass identity, which JIT-creates
the OSAC User, then uses the private admin CLI to grant that user
`tenant-admin` through a RoleBinding and waits for `READY`. The same credentials
are reused for the VM and delete flows. The runner authenticates the OSAC CLI
for VMaaS flows before the first CLI action.
Before running, set it to a tenant that does not already exist. The tenant
create flow requires that the configured tenant does not already exist; the
delete flow requires that it exists. All VMaaS specs skip before browser
actions when `tenant_name` is unset or empty. The setup operations use the
admin test user. The create flow saves the generated break-glass credentials in
`/tmp`, updates the password after first login, and grants that identity
tenant-admin permissions. The delete flow removes the RoleBinding, OSAC User,
tenant, and credential file.

Set the deployment and admin login variables from the [Running](#running) section,
then run the flows:

```bash
export OSAC_UI_BASE_URL=https://osac-ui-<namespace>.<cluster-domain>
export OSAC_ADMIN_USERNAME=<keycloak-user>
read -rs OSAC_ADMIN_PASSWORD && export OSAC_ADMIN_PASSWORD
```

Run the tenant create and delete flows together in `full` mode:

```bash
OSAC_VM_FLOW_MODE=full pnpm --filter @osac/playwright exec playwright test \
  --project=tenant
```

Run the tenant create and delete flows separately:

```bash
OSAC_VM_FLOW_MODE=create pnpm --filter @osac/playwright exec playwright test \
  create.tenant.spec.ts --project=tenant

pnpm --filter @osac/playwright exec playwright test \
  delete.tenant.spec.ts --project=tenant-delete --no-deps
```

### Full VMaaS setup flow

The creation and VM portion of the full-flow journey runs in this order:

1. Create and verify the instance type, disk image, storage tier and its
   storage-backend readiness, and tenant.
2. Create and publish the catalog item using the resources from step 1.
3. Log in with the generated tenant break-glass credentials, set the
   replacement password from `tenant_password` (or the tenant name), grant
   the identity `tenant-admin` permissions through a private-API RoleBinding,
   log in again as the tenant admin, and create a tenant-scoped SSH public-key
   Secret in the Default project.
4. Open the published catalog item and exercise the VM wizard: catalog item,
   project and VM name, disk image, instance type, boot-disk size and storage
   tier, additional disks, default networking, auto external IP, SSH key,
   cloud-init/user-data, and the review page.
5. Submit the wizard and verify navigation to `/vms/<id>`, the
   `ComputeInstance` reaches `Running`, and the VM appears in both the tenant
   VM list and its details page.

The full-flow projects run in this order:

1. `vmaas-full-flow-pre-resource` creates the infrastructure resources, catalog
   item, and tenant.
2. `vm-flow` creates and verifies the VM.
3. `vmaas_full_happy_basic` deletes the VM as the tenant admin.
4. `vmaas-full-flow-pre-resource-delete` removes the catalog item, disk image,
   instance type, and storage tier as the admin user.
5. `tenant-delete` removes the tenant-admin RoleBinding and OSAC User, SSH
   public-key Secret, tenant, generated credential file, and full-flow state.

`vmaas-all-full-flows` is the aggregator for complete VMaaS flows. It currently
depends on `vmaas_full_happy_basic`; add future full-flow projects to its
dependencies in `playwright.config.ts`.

Run the complete creation flow, including VM creation, with:

```bash
OSAC_VM_FLOW_MODE=create pnpm --filter @osac/playwright exec playwright test \
  --project=vm-flow
```

Run the complete deletion flow for an existing creation run with:

```bash
OSAC_VM_FLOW_MODE=delete pnpm --filter @osac/playwright exec playwright test \
  --project=vmaas-full-flow-pre-resource
```

In delete mode, `vmaas-full-flow-pre-resource` depends on `vm-flow`, so VM
deletion runs first. The resource cleanup and tenant deletion run only after
that VM cleanup succeeds.

The creation command stores the non-secret run IDs in
`/tmp/osac-vmaas-full-flow-state-<instance>.json`. The deletion-only command
reuses those IDs and must run on the same host with the same `--instance`
value. They expect the VM, generated tenant
credentials, and full-flow resources to already exist. If the creation run
failed, the state file remains available for cleanup. VM deletion is
idempotent when the tenant VM list is already empty; a different VM in the
list is reported as a run-state mismatch instead of being deleted.

The full happy-flow project deletes the VM after the complete creation chain.
Its pre-resource teardown then removes the remaining resources.

Run the complete creation and deletion flow with:

```bash
OSAC_VM_FLOW_MODE=full pnpm --filter @osac/playwright exec playwright test \
  --project=vmaas-all-full-flows
```

The flow uses the same values from `apps/e2e-tests/extravars.yaml` as the
individual create projects. The catalog item uses the configured template and
the instance-indexed instance type and disk image created by the resource project. The
runner stops after the first failure, including a preflight failure because a
resource already exists, so the remaining flow steps are not started. Use
`--headed` when a graphical display is available:

```bash
pnpm --filter @osac/playwright exec playwright test \
  --project=vm-flow --headed
```

### Storage tier flow tests

The Storage tier specs read `storage_tier.name`, `description`, `backend`, and
`protocol` from `apps/e2e-tests/extravars.yaml`. The configured backend must
already exist. The create test checks that it is Ready and uses the `lvms`
provider on Storage Backends, then verifies that it is selectable in the create
form. The runner appends `--instance=INDEX` to `storage_tier.name`; use the
same instance value for separate create and delete commands.

The project run has two steps:

1. `storage-tier` creates the Storage tier and verifies its configuration.
2. `storage-tier-delete` deletes the configured Storage tier after the
   create flow completes.

Set the common variables from the [Running](#running) section, then run the
complete flow from the repository root:

```bash
pnpm --filter @osac/playwright exec playwright test --project=storage-tier
```

Playwright runs the create project followed by its teardown. Add `--headed` if
a graphical display is available:

```bash
pnpm --filter @osac/playwright exec playwright test \
  --project=storage-tier --headed
```

To run Storage tier create and delete separately, export the same run ID before
both commands. The specs check `storage_tier.name` and skip if it is unset;
they do not use `tenant_name` or create or delete a tenant:

```bash
export OSAC_E2E_INSTANCE=1

pnpm --filter @osac/playwright exec playwright test \
  create.storage_tier.spec.ts --project=storage-tier --no-deps

pnpm --filter @osac/playwright exec playwright test \
  delete.storage_tier.spec.ts --project=storage-tier-delete --no-deps
```

The `--no-deps` option skips the create project's automatic teardown, so the
explicit delete project runs after the separate create command. The delete
test creates the configured tier from `extravars.yaml` if it is absent, then
deletes the tier identified by the instance-indexed name. Use the same
`OSAC_E2E_INSTANCE` to target a tier from an interrupted run.

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
export OSAC_ADMIN_USERNAME=<keycloak-user>
read -rs OSAC_ADMIN_PASSWORD && export OSAC_ADMIN_PASSWORD
```

Run the disk image lifecycle flow:

```bash
pnpm --filter @osac/playwright exec playwright test \
  --project=disk-image-lifecycle
```

The `disk-image-lifecycle` project depends on `disk-image`. The
create project's teardown deletes the image after the lifecycle project
finishes, including when the lifecycle test fails. To run only the delete
project, use `--project=disk-image-delete --no-deps`. If the configured
image is absent, the delete test creates it from `extravars.yaml` and then
deletes it.
The runner appends `--instance=INDEX` to `disk_image.name`, and the full name
appears in the test title. Use the same instance value to clean up an
interrupted run.

### Instance type flow tests

The instance type tests read their hard-coded test data from
`apps/e2e-tests/extravars.yaml`. Change the `instance_type` values there before
running the flow if you need a different name, description, vCPU/memory values,
or GPU configuration. The specs skip if `instance_type.name` is unset. The
create test fails clearly if that instance type already exists. The runner
appends `--instance=INDEX` to the configured instance type name.

The tests run in this dependency order:

1. `instance-type` creates the instance type and verifies it is `Active`.
2. `instance-type-lifecycle` verifies the `Deprecate`, `Obsolete`, and
   `Reactivate` transitions.
3. `instance-type-delete` deletes the instance type and waits for its row
   to disappear.

Set the common variables from the [Running](#running) section, then run the
complete instance type flow from the `osac-ui/` workspace root:

```bash
pnpm --filter @osac/playwright exec playwright test \
  --project=instance-type-lifecycle
```

Playwright runs the create dependency, then runs the create project's teardown
to delete the instance type after the lifecycle test. Add `--headed` to the
command when a graphical display is available:

```bash
pnpm --filter @osac/playwright exec playwright test \
  --project=instance-type-lifecycle --headed
```

To run the three instance-type tests separately, run them in this order:

```bash
pnpm --filter @osac/playwright exec playwright test \
  create.instance_type.spec.ts --project=instance-type --no-deps

pnpm --filter @osac/playwright exec playwright test \
  lifecycle.instance_type.spec.ts --project=instance-type-lifecycle --no-deps

pnpm --filter @osac/playwright exec playwright test \
  delete.instance_type.spec.ts --project=instance-type-delete --no-deps
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
  --project=storage-tier --project=instance-type-lifecycle
```

### Catalog UI flow tests

The catalog flow uses the admin UI wizard to create and publish the catalog
item, then verifies it on the Catalog page. Deletion opens the item details
page and uses its confirmation dialog. Set `OSAC_UI_BASE_URL`,
`OSAC_ADMIN_USERNAME`, and `OSAC_ADMIN_PASSWORD` before running the tests.
The runner appends `--instance=INDEX` to the catalog, instance type, and disk
image names used by this flow.

The catalog configuration is in `apps/e2e-tests/extravars.yaml`:

| Parameter                           | Description                                                       |
| ----------------------------------- | ----------------------------------------------------------------- |
| `catalog.name`                      | Unique catalog item name to create and delete                     |
| `catalog.template`                  | Existing compute instance template referenced by the catalog item |
| `catalog.description`               | Catalog description                                               |
| `catalog.fields.instance_type.name` | Instance type created for this catalog flow                       |
| `catalog.fields.boot_disk.size_gib` | Boot-disk storage value displayed for the catalog item            |
| `catalog.fields.disk_image.name`    | Disk image created for this catalog flow                          |

Run creation and deletion together. The catalog project creates its configured
instance type and disk image as dependencies, then removes them after catalog
teardown. Both catalog operations verify the Catalog page:

```bash
export OSAC_UI_BASE_URL=https://osac-ui-<namespace>.<cluster-domain>

pnpm --filter @osac/playwright exec playwright test \
  --project=catalog
```

Run the flows separately:

```bash
pnpm --filter @osac/playwright exec playwright test \
  create.catalog.spec.ts --project=catalog

pnpm --filter @osac/playwright exec playwright test \
  delete.catalog.spec.ts --project=catalog-delete --no-deps
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
`OSAC_ADMIN_USERNAME`/`OSAC_ADMIN_PASSWORD`.

```bash
export OSAC_UI_BASE_URL=http://localhost:5173 OSAC_ADMIN_USERNAME=...
read -rs OSAC_ADMIN_PASSWORD && export OSAC_ADMIN_PASSWORD
pnpm playwright
```

The backing `fulfillment-service`/Keycloak for a local dev server is often a
dev/lab cluster with a self-signed or cluster-internal CA cert — the real
login redirect goes there even though the UI itself is on `localhost`, so if
you hit a TLS error, explicitly opt in with `IGNORE_HTTPS_ERRORS=true` rather
than trusting it by default:

```bash
export OSAC_UI_BASE_URL=http://localhost:5173 OSAC_ADMIN_USERNAME=... IGNORE_HTTPS_ERRORS=true
read -rs OSAC_ADMIN_PASSWORD && export OSAC_ADMIN_PASSWORD
pnpm playwright
```

## How authentication works

`osac-ui` has no test-mode auth bypass — every login goes through a real
Keycloak realm via the app's normal OIDC flow. Rather than repeating that
flow (and Keycloak's own login page) for every test, this harness follows
Playwright's standard pattern for testing authenticated apps:

1. A `setup` project (`src/auth.setup.ts`) opens the app, which auto-redirects
   to Keycloak when unauthenticated, fills in the admin credentials from
   `OSAC_ADMIN_USERNAME`/`OSAC_ADMIN_PASSWORD` on Keycloak's real login form,
   and saves the resulting browser session
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
export OSAC_UI_BASE_URL=... OSAC_ADMIN_USERNAME=...
read -rs OSAC_ADMIN_PASSWORD && export OSAC_ADMIN_PASSWORD
pnpm playwright
```

`pnpm playwright` runs every spec under `scratch/` — delete the file when
you're done with it. Gitignore keeps it out of git either way, but it does
not stop Playwright from finding and re-running a stale spec next time.
