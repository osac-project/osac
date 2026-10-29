import { type Locator, type Page, expect } from '@playwright/test';

import { getTableRowByCellText } from './general/row_assertions';
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
  await expect(
    page.locator(...VmDetailsPage.statusLabel).filter({ hasText: exactText('Running') }),
  ).toHaveCount(1, { timeout });
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
  await expect(deleteDialog).toBeVisible();
  await expect(deleteDialog.locator(...VmDetailsPage.deleteTitle)).toContainText(`Delete ${name}?`);
  await expect(deleteDialog.getByText(...VmDetailsPage.deleteWarning)).toBeVisible();
  await deleteDialog.getByRole(...VmDetailsPage.deleteButton).click();
  await expect(deleteDialog).toBeHidden();
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
        await expect(table.or(emptyState)).toBeVisible();
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
