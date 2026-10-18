import { type Page, expect } from '@playwright/test';

import {
  StorageMainPage,
  StorageTierCreatePage,
  StorageTierDeletePage,
  StorageTierDetailsPage,
} from '../../locators/storage';

export type StorageTierProtocol = 'NFS' | 'Block';

export interface StorageTierCreateParams {
  name: string;
  description: string;
  backend: string;
  protocol: StorageTierProtocol;
}

/** Default tier values for the Storage tier create/delete flow. */
export const localStorageTier: StorageTierCreateParams = {
  name: 'storage-tier-e2e',
  description: 'Storage tier created by Playwright.',
  backend: 'local',
  protocol: 'Block',
};

const storageTierTable = (page: Page) => page.getByRole(...StorageMainPage.tiersTable);

const storageTierLink = (page: Page, name: string) =>
  storageTierTable(page).getByRole('link', { name, exact: true });

const storageTierRow = (page: Page, name: string) =>
  storageTierTable(page)
    .getByRole('row')
    .filter({ has: page.getByRole('link', { name, exact: true }) });

const getStorageTierNames = async (page: Page): Promise<string[]> =>
  (await storageTierTable(page).getByRole('link').allTextContents())
    .map((name) => name.trim())
    .filter(Boolean);

/** Select Backends and verify that the named backend appears in the table. */
export const expectStorageBackendListed = async (
  page: Page,
  backendName: string,
): Promise<void> => {
  await page.getByRole(...StorageMainPage.backendsTab).click();
  const backendsTable = page.getByRole(...StorageMainPage.backendsTable);
  await expect(backendsTable).toBeVisible();
  await expect(backendsTable.getByRole('link', { name: backendName, exact: true })).toBeVisible();
};

/** Select Tiers and verify that the named tier appears in the table. */
export const expectStorageTierListed = async (
  page: Page,
  tierName: string,
  timeout = 15_000,
): Promise<void> => {
  const tiersTab = page.getByRole(...StorageMainPage.tiersTab);
  if (await tiersTab.isVisible()) {
    await tiersTab.click();
  }
  await expect(storageTierTable(page)).toBeVisible();
  try {
    await expect(storageTierLink(page, tierName)).toBeVisible({ timeout });
  } catch {
    const names = await getStorageTierNames(page);
    const availableTiers = names.length ? names.map((name) => `"${name}"`).join(', ') : 'none';
    throw new Error(
      `Storage tier "${tierName}" was not found in the Tiers table. ` +
        `Available tiers: ${availableTiers}.`,
    );
  }
};

/** Return from the created tier details to the tier list. */
export const openStorageTierListPage = async (page: Page): Promise<void> => {
  const tiersListBreadcrumb = page.getByRole(...StorageTierDetailsPage.tiersListBreadcrumb);
  await expect(tiersListBreadcrumb).toBeVisible();
  await tiersListBreadcrumb.click();
  await expect(storageTierTable(page)).toBeVisible();
};

/** Verify that the named tier is absent from the tiers table. */
export const expectStorageTierNotListed = async (page: Page, tierName: string): Promise<void> => {
  if ((await storageTierLink(page, tierName).count()) > 0) {
    throw new Error(
      `Cannot create storage tier "${tierName}": a tier with this name already exists.`,
    );
  }
};

/** Wait for a deleted tier to disappear and report its remaining status if it does not. */
export const expectStorageTierRemoved = async (
  page: Page,
  tierName: string,
  timeout = 120_000,
): Promise<void> => {
  const tierRow = storageTierRow(page, tierName);
  try {
    await expect(storageTierLink(page, tierName)).toHaveCount(0, { timeout });
  } catch {
    const remainingRows = await tierRow.count();
    const currentStatus =
      remainingRows === 1
        ? ((await tierRow.getByRole('gridcell').nth(1).textContent())?.trim() ?? 'unknown')
        : 'unknown';
    throw new Error(
      `Storage tier "${tierName}" was not removed from the Tiers table within ` +
        `${Math.round(timeout / 1000)} seconds after deletion. ` +
        `The row is still present with status "${currentStatus}".`,
    );
  }
};

/** Open the tier creation form and verify its title and required inputs. */
export const openCreateStorageTierPage = async (page: Page): Promise<void> => {
  const createTier = page.getByRole(...StorageMainPage.createTier);
  await expect(createTier).toBeVisible();
  await createTier.click();

  await expect(page.getByRole(...StorageTierCreatePage.pageTitle)).toBeVisible();
  await expect(page.getByLabel(...StorageTierCreatePage.name)).toBeVisible();
  await expect(page.getByLabel(...StorageTierCreatePage.description)).toBeVisible();
  await expect(page.getByLabel(...StorageTierCreatePage.backend)).toBeVisible();
  await expect(page.getByRole(...StorageTierCreatePage.nfsProtocol)).toBeVisible();
  await expect(page.getByRole(...StorageTierCreatePage.blockProtocol)).toBeVisible();
};

/** Create a storage tier using an already-existing backend. */
export const createStorageTier = async (
  page: Page,
  tier: StorageTierCreateParams,
): Promise<void> => {
  await page.getByLabel(...StorageTierCreatePage.name).fill(tier.name);
  await page.getByLabel(...StorageTierCreatePage.description).fill(tier.description);

  await page.getByLabel(...StorageTierCreatePage.backend).click();
  await page.getByRole('option', { name: tier.backend, exact: true }).click();
  if (tier.protocol === 'NFS') {
    await page.getByRole(...StorageTierCreatePage.nfsProtocol).check();
  } else {
    await page.getByRole(...StorageTierCreatePage.blockProtocol).check();
  }
  await page.getByRole(...StorageTierCreatePage.create).click();
  try {
    await expect(page.getByRole('heading', { name: tier.name, exact: true })).toBeVisible();
  } catch {
    throw new Error(
      `After submitting the form, the details page for storage tier "${tier.name}" did not open.`,
    );
  }
};

/** Verify the selected backend and protocol in a tier's table row. */
export const expectStorageTierConfiguration = async (
  page: Page,
  tier: StorageTierCreateParams,
): Promise<void> => {
  const row = storageTierRow(page, tier.name);
  await expect(row.getByText(tier.backend, { exact: true })).toBeVisible();
  await expect(row.getByText(tier.protocol, { exact: true })).toBeVisible();
};

/** Open the named tier's row menu, confirm deletion, and verify it disappears. */
export const deleteStorageTier = async (page: Page, tierName: string): Promise<void> => {
  await expectStorageTierListed(page, tierName, 3_000);

  const row = storageTierRow(page, tierName);
  const actionsButton = row.getByRole('button', {
    name: `Actions for ${tierName}`,
    exact: true,
  });
  try {
    await expect(actionsButton).toBeVisible({ timeout: 3_000 });
  } catch {
    throw new Error(
      `Cannot delete storage tier "${tierName}": its Actions menu button was not available.`,
    );
  }
  await actionsButton.click();
  await page.getByRole(...StorageTierDeletePage.deleteMenuItem).click();

  const deleteDialog = page.locator(...StorageTierDeletePage.deleteDialog);
  await expect(deleteDialog).toBeVisible();
  await expect(deleteDialog.getByRole('heading')).toContainText(`Delete ${tierName}?`);
  await expect(deleteDialog.getByText(...StorageTierDeletePage.deleteWarning)).toBeVisible();

  await deleteDialog.getByRole(...StorageTierDeletePage.confirmDelete).click();
  await expect(deleteDialog).toBeHidden();
  await expectStorageTierRemoved(page, tierName);
};
