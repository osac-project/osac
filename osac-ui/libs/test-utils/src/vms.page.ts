import { type Locator, type Page, expect } from '@playwright/test';

import { confirmDetailDeletion, getTableRowByCellText } from './general/row_assertions';
import { openMainPageOption } from './main.page';
import { VmDetailsActionButtons, VmDetailsPage, VmsMainPage } from '../../locators/vms';

/** Escape a value before placing it in an exact-match regular expression. */
const escapeRegExp = (value: string): string => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/** Build an exact, whitespace-tolerant text matcher. */
const exactText = (value: string): RegExp =>
  new RegExp(`^\\s*${escapeRegExp(value.normalize('NFC'))}\\s*$`);

const getVmDetailsStatus = (page: Page): Locator =>
  page
    .getByRole(...VmDetailsPage.statusTerm)
    .filter({ hasText: exactText('Status') })
    .locator('..')
    .locator(...VmDetailsPage.statusLabel);

export type VmPowerAction = 'start' | 'stop' | 'restart';

type VmPowerActionButton = readonly ['button', { name: string; exact: true }];

const vmPowerActionButton = (action: VmPowerAction): VmPowerActionButton =>
  VmDetailsActionButtons[action] as VmPowerActionButton;

export interface VmResizeTarget {
  name: string;
  vcpus: number;
  memoryGib: number;
}

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

/** Select the table view so VM rows are available to list and deletion helpers. */
export const selectVmListView = async (page: Page): Promise<void> => {
  const listView = page.locator(...VmsMainPage.listView);
  await expect(listView).toBeVisible();
  if ((await listView.getAttribute('aria-pressed')) !== 'true') {
    await listView.click();
  }
  await expect(listView).toHaveAttribute('aria-pressed', 'true');
};

/** Wait until the tenant VM list reports a loaded table or empty state. */
const waitForVmListReady = async (page: Page): Promise<Locator> => {
  await selectVmListView(page);
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
  return table;
};

/** Return the VM names currently visible in the tenant VM list. */
export const listVmNames = async (page: Page): Promise<string[]> => {
  await selectVmListView(page);
  const table = await waitForVmListReady(page);
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
  await selectVmListView(page);
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
  await expect(getVmDetailsStatus(page).filter({ hasText: exactText('Running') })).toHaveCount(1, {
    timeout,
  });
};

/** Wait for the VM details page to report Running, refreshing the UI between polls. */
export const waitForVmToBeRunning = async (
  page: Page,
  name: string,
  timeout = 180_000,
): Promise<void> => {
  await waitForVmDetailsStatus(page, name, 'Running', timeout);
};

/**
 * Poll the VM details page until it reports the requested lifecycle state, or
 * one of several states, and return the state it reported.
 */
export const waitForVmDetailsStatus = async (
  page: Page,
  name: string,
  status: string | readonly string[],
  timeout = 180_000,
): Promise<string> => {
  const statuses = typeof status === 'string' ? [status] : status;
  return waitForVmDetails(page, name, statuses, timeout, async () => {
    for (const candidate of statuses) {
      const statusLabel = getVmDetailsStatus(page).filter({ hasText: exactText(candidate) });
      if ((await statusLabel.count()) > 0) {
        return candidate;
      }
    }
    return '';
  });
};

/** Trigger a VM power action; the caller verifies the resulting lifecycle state. */
export const runVmPowerAction = async (page: Page, action: VmPowerAction): Promise<void> => {
  await page.getByRole(...vmPowerActionButton(action)).click();
};

const vmPowerActionToastTitles: Record<VmPowerAction, string> = {
  start: 'Start initiated for',
  stop: 'Stop initiated for',
  restart: 'Restart initiated for',
};

/** Verify which power buttons the VM details page enables. */
export const expectVmPowerButtons = async (
  page: Page,
  enabled: Record<VmPowerAction, boolean>,
): Promise<void> => {
  for (const [action, isEnabled] of Object.entries(enabled) as [VmPowerAction, boolean][]) {
    const button = page.getByRole(...vmPowerActionButton(action));
    await (isEnabled ? expect(button).toBeEnabled() : expect(button).toBeDisabled());
  }
};

/** Click a power button on the VM details page and verify the success toast. */
export const runVmPowerActionWithToast = async (
  page: Page,
  name: string,
  action: VmPowerAction,
): Promise<void> => {
  await expectVmDetailsPage(page, name);
  await expect(page.getByRole(...vmPowerActionButton(action))).toBeEnabled();
  await runVmPowerAction(page, action);
  // The alert title carries a screen-reader prefix, so match a substring.
  await expect(page.getByText(`${vmPowerActionToastTitles[action]} ${name}`)).toBeVisible();
};

const detailValue = (page: Page, label: string): Locator =>
  page
    .locator('dt')
    .filter({ hasText: exactText(label) })
    .locator('..')
    .locator('dd');

/** Verify the instance type and the sizing values shown on the VM details page. */
export const getVmConfiguration = async (page: Page): Promise<VmResizeTarget> => ({
  name: (await detailValue(page, 'Instance type').textContent())?.trim() ?? '',
  vcpus: Number((await detailValue(page, 'vCPUs').textContent())?.trim()),
  memoryGib: Number(
    (await detailValue(page, 'Memory').textContent())?.trim().replace(/\s+GiB$/, ''),
  ),
});

/** Verify the instance type and the sizing values shown on the VM details page. */
export const expectVmConfiguration = async (
  page: Page,
  instanceType: VmResizeTarget,
): Promise<void> => {
  await expect
    .poll(
      async () => {
        return getVmConfiguration(page);
      },
      {
        message: `Wait for VM sizing to show ${instanceType.name} (${instanceType.vcpus} vCPUs, ${instanceType.memoryGib} GiB)`,
        timeout: 60_000,
        intervals: [1_000, 2_000, 5_000],
      },
    )
    .toMatchObject({
      name: expect.stringContaining(instanceType.name),
      vcpus: instanceType.vcpus,
      memoryGib: instanceType.memoryGib,
    });
};

/** Verify a condition row rendered in the VM details Conditions table. */
export const expectVmConditionStatus = async (
  page: Page,
  conditionType: string,
  status: string,
): Promise<void> => {
  const table = page.getByRole(...VmDetailsPage.conditionsTable);
  const row = table.locator('tbody tr').filter({ hasText: conditionType });
  await expect(row).toHaveCount(1);
  await expect(row.locator('td[data-label="Status"]')).toHaveText(exactText(status));
};

/** Poll the VM details page until a condition reaches the requested status. */
export const waitForVmConditionStatus = async (
  page: Page,
  name: string,
  conditionType: string,
  status: string,
  timeout = 60_000,
): Promise<void> => {
  await waitForVmDetails(page, name, status, timeout, async () => {
    await expectVmConditionStatus(page, conditionType, status);
    return status;
  });
};

const waitForVmDetails = async (
  page: Page,
  name: string,
  status: string | readonly string[],
  timeout: number,
  assertion: () => Promise<string>,
): Promise<string> => {
  const statuses = typeof status === 'string' ? [status] : status;
  let reported = '';
  await expect
    .poll(
      async () => {
        try {
          if (/\/vms\/[^/]+$/.test(new URL(page.url()).pathname)) {
            await page.reload();
          } else {
            await openVmsPage(page);
            await openVmDetailsPage(page, name);
          }
          await expectVmDetailsPage(page, name);
          reported = await assertion();
        } catch {
          reported = '';
        }
        return statuses.includes(reported);
      },
      {
        message: `Wait for VM '${name}' to report ${statuses.join(' or ')}`,
        timeout,
        intervals: [1_000, 2_000, 5_000],
      },
    )
    .toBe(true);
  return reported;
};

/** Change the VM instance type through the details-page modal. */
export const resizeVmInstanceType = async (page: Page, target: VmResizeTarget): Promise<void> => {
  await page.getByRole(...VmDetailsPage.instanceTypeEditButton).click();
  const dialog = page.getByRole('dialog', { name: 'Edit instance type' });
  await expect(dialog).toBeVisible();

  await dialog.getByRole('combobox').click();
  // PatternFly renders the open typeahead menu in a portal outside the dialog.
  // Keep the dialog scope for the form controls, but locate the option globally.
  const option = page.getByRole('option', {
    name: new RegExp(
      `^${escapeRegExp(target.name)}\\s+—\\s+${target.vcpus} vCPUs, ${target.memoryGib} GiB$`,
    ),
  });
  await expect(option).toBeVisible();
  await option.click();
  await dialog.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await page.reload();
  await expect(page.getByRole(...VmDetailsPage.instanceTypeEditButton)).toBeVisible();
};

/** Open a VM details page from its exact row link. */
export const openVmDetailsPage = async (page: Page, name: string): Promise<void> => {
  await selectVmListView(page);
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

/** Delete a VM when it exists and wait until the tenant list no longer returns it. */
export const deleteVmIfPresent = async (page: Page, name: string): Promise<boolean> => {
  if (/\/vms\/[^/]+$/.test(new URL(page.url()).pathname)) {
    await deleteVm(page, name);
  } else {
    await openVmsPage(page);
    const names = await listVmNames(page);
    if (!names.includes(name)) {
      return false;
    }
    await openVmDetailsPage(page, name);
    await deleteVm(page, name);
  }
  await waitForVmToBeDeleted(page, name);
  return true;
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

        await selectVmListView(page);
        const table = await waitForVmListReady(page);
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
