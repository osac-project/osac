import { type Locator, type Page, expect } from '@playwright/test';

import { confirmDetailDeletion, getTableRowByCellText } from './general/row_assertions';
import { openMainPageOption } from './main.page';
import { VmDetailsPage, VmsMainPage } from '../../locators/vms';

const escapeRegExp = (value: string): string => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

const exactText = (value: string): RegExp =>
  new RegExp(`^\\s*${escapeRegExp(value.normalize('NFC'))}\\s*$`);

const getVmRow = (page: Page, name: string): Locator =>
  getTableRowByCellText(
    page.getByRole(...VmsMainPage.vmsTable),
    VmsMainPage.vmRows,
    VmsMainPage.vmNameCell,
    exactText(name),
  );

/** Open the tenant Virtual Machines page and verify its title. */
export const openVmsPage = async (page: Page): Promise<void> => {
  await openMainPageOption(page, 'virtualMachines');
  await expect(page).toHaveURL((url) => url.pathname === '/vms');
  await expect(page.getByRole(...VmsMainPage.pageTitle)).toBeVisible();
};

/** Return the VM names currently visible in the tenant VM list. */
export const listVmNames = async (page: Page): Promise<string[]> => {
  const table = page.getByRole(...VmsMainPage.vmsTable);
  const emptyState = page.getByText(...VmsMainPage.emptyState);
  const filteredEmptyState = page.getByText(...VmsMainPage.filteredEmptyState);
  const noMatchingState = page.getByText(...VmsMainPage.noMatchingState);
  const errorState = page.getByRole(...VmsMainPage.errorState);
  await expect
    .poll(
      async () => {
        if (await errorState.isVisible().catch(() => false)) {
          throw new Error('The tenant VM list reported an API error while loading.');
        }
        if (await table.isVisible().catch(() => false)) {
          return 'table';
        }
        if (await emptyState.isVisible().catch(() => false)) {
          return 'empty';
        }
        if (await filteredEmptyState.isVisible().catch(() => false)) {
          return 'filtered-empty';
        }
        if (await noMatchingState.isVisible().catch(() => false)) {
          return 'no-matching';
        }
        return '';
      },
      { timeout: 15_000, intervals: [250, 500, 1_000] },
    )
    .toBeTruthy();
  if ((await table.count()) === 0) {
    return [];
  }

  return table
    .locator(...VmsMainPage.vmNameCell)
    .getByRole('link')
    .allTextContents()
    .then((names) => names.map((name) => name.trim()).filter(Boolean));
};

/** Verify that a VM row appears in the tenant VM list. */
export const expectVmToExist = async (
  page: Page,
  name: string,
  timeout = 15_000,
): Promise<Locator> => {
  const row = getVmRow(page, name);
  await expect(row).toHaveCount(1, { timeout });
  return row;
};

/** Verify that a VM row has reached the Running state. */
export const expectVmToBeRunning = async (
  page: Page,
  name: string,
  timeout = 15_000,
): Promise<void> => {
  const row = await expectVmToExist(page, name, timeout);
  await expect(row.locator(...VmsMainPage.vmStatusCell)).toHaveText('Running', { timeout });
};

/** Verify the VM details route and title. */
export const expectVmDetailsPage = async (
  page: Page,
  name: string,
  timeout = 15_000,
): Promise<void> => {
  await expect(page).toHaveURL(/\/vms\/[^/]+$/, { timeout });
  await expect(page.locator(...VmDetailsPage.pageTitle)).toHaveText(exactText(name), { timeout });
};

/** Verify that the VM details page reports Running. */
export const expectVmDetailsToBeRunning = async (page: Page, timeout = 15_000): Promise<void> => {
  // The status is shown both beside the title and in the Overview card.
  await expect(
    page
      .locator(...VmDetailsPage.statusLabel)
      .filter({ hasText: exactText('Running') })
      .first(),
  ).toBeVisible({ timeout });
};

/** Wait for the VM details page to report Running, refreshing the UI between polls. */
export const waitForVmToBeRunning = async (
  page: Page,
  name: string,
  timeout = 180_000,
): Promise<void> => {
  await expect
    .poll(
      async () => {
        await page.reload();
        await expectVmDetailsPage(page, name);
        const runningStatus = page
          .locator(...VmDetailsPage.statusLabel)
          .filter({ hasText: exactText('Running') });
        return (await runningStatus.count()) > 0 ? 'Running' : '';
      },
      { timeout, intervals: [1_000, 2_000, 5_000] },
    )
    .toBe('Running');
};

/** Open a VM details page from its exact row link. */
export const openVmDetailsPage = async (page: Page, name: string): Promise<void> => {
  const row = await expectVmToExist(page, name);
  await row
    .locator(...VmsMainPage.vmNameCell)
    .getByRole('link', { name, exact: true })
    .click();
  await expectVmDetailsPage(page, name);
};

/** Delete a VM from its details page and wait for navigation to the list. */
export const deleteVm = async (page: Page, name: string): Promise<void> => {
  await expectVmDetailsPage(page, name);
  await page.getByRole(...VmDetailsPage.deleteButton).click();

  const deleteDialog = page.locator(...VmDetailsPage.deleteDialog);
  await confirmDetailDeletion(
    deleteDialog,
    deleteDialog.locator(...VmDetailsPage.deleteTitle),
    deleteDialog.getByText(...VmDetailsPage.deleteWarning),
    deleteDialog.getByRole(...VmDetailsPage.deleteButton),
    name,
  );
  await expect(page).toHaveURL((url) => url.pathname === '/vms');
  await expect(page.getByRole(...VmsMainPage.pageTitle)).toBeVisible();
};

/** Poll the tenant VM list until a deleted VM is no longer returned. */
export const waitForVmToBeDeleted = async (
  page: Page,
  name: string,
  timeout = 180_000,
): Promise<void> => {
  await expect
    .poll(
      async () => {
        await page.reload();
        await expect(page).toHaveURL((url) => url.pathname === '/vms');
        await expect(page.getByRole(...VmsMainPage.pageTitle)).toBeVisible();

        const table = page.getByRole(...VmsMainPage.vmsTable);
        const emptyState = page.getByText(...VmsMainPage.emptyState);
        const filteredEmptyState = page.getByText(...VmsMainPage.filteredEmptyState);
        const noMatchingState = page.getByText(...VmsMainPage.noMatchingState);
        const errorState = page.getByRole(...VmsMainPage.errorState);
        await expect
          .poll(
            async () => {
              if (await errorState.isVisible().catch(() => false)) {
                throw new Error('The tenant VM list reported an API error while loading.');
              }
              if (await table.isVisible().catch(() => false)) {
                return 'table';
              }
              if (await emptyState.isVisible().catch(() => false)) {
                return 'empty';
              }
              if (await filteredEmptyState.isVisible().catch(() => false)) {
                return 'filtered-empty';
              }
              if (await noMatchingState.isVisible().catch(() => false)) {
                return 'no-matching';
              }
              return '';
            },
            { timeout: 15_000, intervals: [250, 500, 1_000] },
          )
          .toBeTruthy();
        if ((await table.count()) === 0) {
          return 0;
        }
        await expect(table).toBeVisible();
        return await getVmRow(page, name).count();
      },
      { timeout, intervals: [1_000, 2_000, 5_000] },
    )
    .toBe(0);
};
