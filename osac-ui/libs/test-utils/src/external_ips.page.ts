import { type Page, expect } from '@playwright/test';

import { selectOption } from './general/form_controls';
import { logProgress } from './general/test_progress';
import { openExternalIpPoolsPage, openExternalIpsPage } from './main.page';
import {
  ExternalIpCreatePage,
  ExternalIpDeleteDialog,
  ExternalIpPoolCreatePage,
  ExternalIpPoolsPage,
  ExternalIpsPage,
} from '../../locators/external_ips';

export interface ExternalIpCreateParams {
  name: string;
  poolName: string;
  project?: string;
}

export interface ExternalIpPoolCreateParams {
  name: string;
  cidr: string;
}

const externalIpTableColumns = [
  'Name',
  'Status',
  'Project',
  'Attached to',
  'Address',
  'IP pool',
  'Created',
] as const;

/**
 * The External IPs list is a Connect call to the private ExternalIPs service.
 * The glob keeps session, project and pool lookups untouched so only the list
 * request fails.
 */
const externalIpListRoute = '**/api/fulfillment/**/ExternalIPs/**';

const externalIpTable = (page: Page) => page.getByRole(...ExternalIpsPage.externalIpsTable);

const externalIpPoolTable = (page: Page) => page.getByRole(...ExternalIpPoolsPage.poolsTable);

const externalIpRow = (page: Page, name: string) =>
  externalIpTable(page)
    .getByRole('row')
    .filter({ has: page.getByRole('cell', { name, exact: true }) });

const externalIpPoolRow = (page: Page, name: string) =>
  externalIpPoolTable(page)
    .getByRole('row')
    .filter({ has: page.getByRole('link', { name, exact: true }) });

const rowActionsToggle = (page: Page, name: string) =>
  page.getByRole('button', { name: `Actions for ${name}`, exact: true });

/** Open the External IPs list page through the Networking navigation group. */
export const openExternalIpListPage = async (page: Page): Promise<void> => {
  await openExternalIpsPage(page);
  await expect(page.getByRole(...ExternalIpsPage.pageTitle)).toBeVisible();
};

/** Open the admin External IP pools page through the Infrastructure group. */
export const openExternalIpPoolListPage = async (page: Page): Promise<void> => {
  await openExternalIpPoolsPage(page);
  await expect(page.getByRole(...ExternalIpPoolsPage.pageTitle)).toBeVisible();
};

/** Wait until the list settles on rows, an empty state, or an error. */
export const waitForExternalIpList = async (page: Page): Promise<void> => {
  await expect
    .poll(
      async () => {
        if ((await page.getByRole(...ExternalIpsPage.loadErrorTitle).count()) > 0) {
          return 'error';
        }
        if ((await externalIpTable(page).count()) > 0) {
          return 'table';
        }
        if ((await page.getByText(...ExternalIpsPage.emptyStateTitle).count()) > 0) {
          return 'empty';
        }
        return 'loading';
      },
      { timeout: 30_000, message: 'The External IP list did not finish loading.' },
    )
    .not.toBe('loading');
};

/** Verify the list renders the documented columns. */
export const expectExternalIpListColumns = async (page: Page): Promise<void> => {
  await waitForExternalIpList(page);
  if ((await externalIpTable(page).count()) === 0) {
    throw new Error('The External IP list is empty, so its columns cannot be verified.');
  }
  for (const column of externalIpTableColumns) {
    await expect(
      externalIpTable(page).getByRole('columnheader', { name: column, exact: true }),
    ).toBeVisible();
  }
};

/** Verify an External IP row exists and, when supplied, its Project cell. */
export const expectExternalIpListed = async (
  page: Page,
  name: string,
  projectLabel?: string,
): Promise<void> => {
  await waitForExternalIpList(page);
  const row = externalIpRow(page, name);
  await expect(row).toHaveCount(1, { timeout: 30_000 });
  if (projectLabel) {
    await expect(row.getByRole('cell', { name: projectLabel, exact: true })).toHaveCount(1);
  }
};

/** Verify an External IP is absent, failing with a duplicate-resource message. */
export const expectExternalIpNotListed = async (page: Page, name: string): Promise<void> => {
  await waitForExternalIpList(page);
  const row = externalIpRow(page, name);
  const matchingRows = await row.count();
  if (matchingRows > 0) {
    throw new Error(
      `External IP "${name}" already exists (${matchingRows} matching rows). ` +
        'Delete it, or run with a different OSAC_E2E_INSTANCE.',
    );
  }
};

/** Verify the search filter reports no match for the supplied term. */
export const expectExternalIpSearchHasNoMatch = async (page: Page, term: string): Promise<void> => {
  await page.getByRole(...ExternalIpsPage.searchInput).fill(term);
  await expect(page.getByText(...ExternalIpsPage.filteredEmptyStateTitle)).toBeVisible();
};

/** Read the Project cell of a listed External IP, for scoping assertions. */
export const readExternalIpProjectLabel = async (page: Page, name: string): Promise<string> => {
  await expectExternalIpListed(page, name);
  const projectCell = externalIpRow(page, name).locator('td[data-label="Project"]');
  await expect(projectCell).toHaveCount(1);
  const label = (await projectCell.innerText()).trim();
  if (!label) {
    throw new Error(`External IP "${name}" has an empty Project cell.`);
  }
  return label;
};

/** Verify every listed External IP belongs to the expected project. */
export const expectExternalIpListScopedToProject = async (
  page: Page,
  projectLabel: string,
): Promise<void> => {
  await waitForExternalIpList(page);
  const projectCells = externalIpTable(page).locator('td[data-label="Project"]');
  const count = await projectCells.count();
  if (count === 0) {
    throw new Error('The External IP list has no rows, so tenant scoping cannot be verified.');
  }
  for (let index = 0; index < count; index += 1) {
    const project = (await projectCells.nth(index).innerText()).trim();
    if (project !== projectLabel) {
      throw new Error(
        `Tenant isolation is broken: the list shows an External IP in project "${project}" ` +
          `while the tenant-admin session expects "${projectLabel}".`,
      );
    }
  }
};

/** Fail the list request and verify the page renders its error state. */
export const expectExternalIpListLoadError = async (page: Page): Promise<void> => {
  await openExternalIpListPage(page);
  await page.route(externalIpListRoute, (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ code: 'internal', message: 'Injected E2E list failure' }),
    }),
  );
  try {
    await page.reload();
    await expect(page.getByRole(...ExternalIpsPage.loadErrorTitle)).toBeVisible({
      timeout: 30_000,
    });
  } finally {
    await page.unroute(externalIpListRoute);
  }
};

/** Verify status reaches the expected value, reporting the observed row on timeout. */
export const expectExternalIpStatus = async (
  page: Page,
  name: string,
  status: string,
  timeout = 180_000,
): Promise<void> => {
  const row = externalIpRow(page, name);
  try {
    await expect(row.getByText(status, { exact: true })).toBeVisible({ timeout });
  } catch {
    const observed = (
      await row
        .first()
        .innerText()
        .catch(() => '')
    )
      .replace(/\s+/g, ' ')
      .trim();
    throw new Error(
      `External IP "${name}" did not reach status "${status}" within ${timeout / 1_000} seconds. ` +
        `The row currently reads: "${observed || 'unknown'}".`,
    );
  }
};

/** Open the creation wizard from the list page. */
export const openCreateExternalIpPage = async (page: Page): Promise<void> => {
  const createLink = page.getByRole(...ExternalIpsPage.createExternalIp);
  await expect(createLink).toBeVisible();
  await createLink.click();
  await expect(page.getByRole(...ExternalIpCreatePage.pageTitle)).toBeVisible();
};

/** Press Next on the current wizard step. */
const goToNextWizardStep = async (page: Page): Promise<void> => {
  await page.getByRole(...ExternalIpCreatePage.next).click();
};

/** Continue past the General step after entering the requested name and project. */
const submitGeneralStep = async (page: Page, params: ExternalIpCreateParams): Promise<void> => {
  await expect(page.getByRole(...ExternalIpCreatePage.generalStepTitle)).toBeVisible();
  await page.locator(...ExternalIpCreatePage.name).fill(params.name);
  if (params.project) {
    await selectOption(page, ExternalIpCreatePage.project, params.project, 0, true);
  }
  await goToNextWizardStep(page);
  await expect(page.getByRole(...ExternalIpCreatePage.configurationStepTitle)).toBeVisible();
};

/** Select the external IP pool, unless auto-selection already did. */
const submitConfigurationStep = async (page: Page, poolName: string): Promise<void> => {
  const toggle = page.locator(...ExternalIpCreatePage.pool);
  const selectedPool = (await toggle.innerText()).trim();
  if (selectedPool !== poolName) {
    await selectOption(page, ExternalIpCreatePage.pool, poolName, 0, true);
  }
  await goToNextWizardStep(page);
  await expect(page.getByRole(...ExternalIpCreatePage.reviewStepTitle)).toBeVisible();
};

/** Create an External IP through the wizard and land back on the list page. */
export const createExternalIp = async (
  page: Page,
  params: ExternalIpCreateParams,
): Promise<void> => {
  logProgress('external-ip-create', `Creating external IP "${params.name}".`);
  await submitGeneralStep(page, params);
  await submitConfigurationStep(page, params.poolName);

  const review = page.locator(...ExternalIpCreatePage.reviewDetails);
  await expect(review).toContainText(params.name);
  await expect(review).toContainText(params.poolName);

  await page.getByRole(...ExternalIpCreatePage.submit).click();

  // Surface the wizard's own failure instead of a bare navigation timeout: a
  // deployment without provider networking rejects the create with
  // FailedPrecondition, and a pool the cluster cannot serve fails allocation.
  const listTitle = page.getByRole(...ExternalIpsPage.pageTitle);
  const submitError = page.getByText(...ExternalIpCreatePage.submitError);
  await expect(listTitle.or(submitError)).toBeVisible({ timeout: 60_000 });
  if ((await submitError.count()) > 0) {
    const alert = page
      .locator('[class*="c-alert"]')
      .filter({ hasText: ExternalIpCreatePage.submitError[0] })
      .first();
    const detail = (await alert.innerText().catch(() => '')).replace(/\s+/g, ' ').trim();
    throw new Error(
      `Creating external IP "${params.name}" failed: ${detail || 'the wizard reported an error'}. ` +
        'Check that the deployment enables provider networking ' +
        '(global.networking.provisioningEnabled) and that the pool can allocate.',
    );
  }
};

/** Press Next on an untouched form and verify the wizard blocks with an alert. */
export const expectExternalIpCreateValidation = async (page: Page): Promise<void> => {
  await goToNextWizardStep(page);
  await expect(page.getByText(...ExternalIpCreatePage.validationAlert)).toBeVisible();
  await expect(page.getByRole(...ExternalIpCreatePage.generalStepTitle)).toBeVisible();
};

/** Verify the row actions menu offers deletion only, with no edit affordance. */
export const expectExternalIpHasNoEditAction = async (page: Page, name: string): Promise<void> => {
  await rowActionsToggle(page, name).click();
  const menuItems = page.getByRole('menu').getByRole('menuitem');
  await expect(menuItems).toHaveCount(1);
  await expect(page.getByRole(...ExternalIpDeleteDialog.deleteMenuItem)).toBeVisible();
  await expect(page.getByRole('menuitem', { name: /edit/i })).toHaveCount(0);
  await page.keyboard.press('Escape');
};

/** Delete an External IP from its list row and wait for the row to disappear. */
export const deleteExternalIpIfPresent = async (page: Page, name: string): Promise<boolean> => {
  await waitForExternalIpList(page);
  if ((await externalIpRow(page, name).count()) === 0) {
    logProgress('external-ip-delete', `External IP "${name}" is already absent.`);
    return false;
  }

  logProgress('external-ip-delete', `Deleting external IP "${name}".`);
  await rowActionsToggle(page, name).click();
  await page.getByRole(...ExternalIpDeleteDialog.deleteMenuItem).click();

  const dialog = page.locator(...ExternalIpDeleteDialog.dialog);
  await expect(dialog).toBeVisible();
  await expect(page.locator(...ExternalIpDeleteDialog.title)).toContainText(`Delete ${name}?`);
  await expect(page.getByText(...ExternalIpDeleteDialog.warning)).toBeVisible();
  await page.getByRole(...ExternalIpDeleteDialog.confirmDelete).click();
  await expect(dialog).toBeHidden({ timeout: 60_000 });
  await expect(externalIpRow(page, name)).toHaveCount(0, { timeout: 180_000 });
  return true;
};

/** Wait until the pool list settles on rows or an empty state. */
export const waitForExternalIpPoolList = async (page: Page): Promise<void> => {
  await expect
    .poll(
      async () => {
        if ((await externalIpPoolTable(page).count()) > 0) {
          return 'table';
        }
        if ((await page.getByText(...ExternalIpPoolsPage.emptyStateTitle).count()) > 0) {
          return 'empty';
        }
        return 'loading';
      },
      { timeout: 30_000, message: 'The External IP pool list did not finish loading.' },
    )
    .not.toBe('loading');
};

/** Create an admin-only External IP pool through the Infrastructure wizard. */
export const createExternalIpPool = async (
  page: Page,
  params: ExternalIpPoolCreateParams,
): Promise<void> => {
  logProgress('external-ip-pool-create', `Creating external IP pool "${params.name}".`);
  const createLink = page.getByRole(...ExternalIpPoolsPage.createPool);
  await expect(createLink).toBeVisible();
  await createLink.click();
  await expect(page.getByRole(...ExternalIpPoolCreatePage.pageTitle)).toBeVisible();

  await page.locator(...ExternalIpPoolCreatePage.name).fill(params.name);
  await page.locator(...ExternalIpPoolCreatePage.cidr).fill(params.cidr);
  await page.getByRole(...ExternalIpPoolCreatePage.next).click();
  await expect(page.getByRole(...ExternalIpPoolCreatePage.submit)).toBeVisible();
  await page.getByRole(...ExternalIpPoolCreatePage.submit).click();
  // The wizard lands on the new pool's details page; return to the list so the
  // caller can assert the row.
  await expect(page.getByRole(...ExternalIpPoolCreatePage.pageTitle)).toBeHidden({
    timeout: 60_000,
  });
  await openExternalIpPoolListPage(page);
  await waitForExternalIpPoolList(page);
};

/** Wait for a pool status, reporting what the row shows on timeout. */
export const expectExternalIpPoolStatus = async (
  page: Page,
  name: string,
  status: string,
  timeout = 300_000,
): Promise<void> => {
  const row = externalIpPoolRow(page, name);
  try {
    await expect(row.getByText(status, { exact: true })).toBeVisible({ timeout });
  } catch {
    const observed = (
      await row
        .first()
        .innerText()
        .catch(() => '')
    )
      .replace(/\s+/g, ' ')
      .trim();
    throw new Error(
      `External IP pool "${name}" did not reach status "${status}" within ${timeout / 1_000} seconds. ` +
        `The row currently reads: "${observed || 'unknown'}".`,
    );
  }
};

/** Verify an External IP pool is absent, failing with a duplicate-pool message. */
export const expectExternalIpPoolNotListed = async (page: Page, name: string): Promise<void> => {
  await waitForExternalIpPoolList(page);
  const row = externalIpPoolRow(page, name);
  const matchingRows = await row.count();
  if (matchingRows > 0) {
    throw new Error(
      `External IP pool "${name}" already exists (${matchingRows} matching rows). ` +
        'Delete it, or run with a different OSAC_E2E_INSTANCE.',
    );
  }
};

/** Delete an External IP pool row when it exists; a missing pool is not an error. */
export const deleteExternalIpPoolIfPresent = async (page: Page, name: string): Promise<void> => {
  await openExternalIpPoolListPage(page);
  await waitForExternalIpPoolList(page);
  const row = externalIpPoolRow(page, name);
  if ((await row.count()) === 0) {
    logProgress('external-ip-pool-delete', `Pool "${name}" is already absent.`);
    return;
  }

  await rowActionsToggle(page, name).click();
  await page.getByRole(...ExternalIpPoolsPage.deleteMenuItem).click();
  const dialog = page.locator(...ExternalIpDeleteDialog.dialog);
  await expect(dialog).toBeVisible();
  await expect(page.locator(...ExternalIpDeleteDialog.title)).toContainText(`Delete ${name}?`);
  await expect(page.getByText(...ExternalIpPoolsPage.deleteWarning)).toBeVisible();
  await page.getByRole(...ExternalIpPoolsPage.confirmDelete).click();
  await expect(dialog).toBeHidden({ timeout: 60_000 });
  await expect(row).toHaveCount(0, { timeout: 300_000 });
};
