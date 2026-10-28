import { type Page, expect } from '@playwright/test';

import { confirmRowDeletion } from './general/row_assertions';
import { openStorageBackendsPage, openStorageTiersPage } from './main.page';
import {
  StorageBackendsPage,
  StorageTierCreatePage,
  StorageTierDeletePage,
  StorageTierDetailsPage,
  StorageTiersPage,
} from '../../locators/storage';

export type StorageTierProtocol = 'NFS' | 'Block';

export interface StorageTierCreateParams {
  name: string;
  description: string;
  backend: string;
  protocol: StorageTierProtocol;
}

const storageTierTable = (page: Page) => page.getByRole(...StorageTiersPage.tiersTable);

const storageBackendTable = (page: Page) => page.getByRole(...StorageBackendsPage.backendsTable);

const storageBackendRow = (page: Page, name: string) =>
  storageBackendTable(page)
    .getByRole('row')
    .filter({ has: page.getByRole('link', { name, exact: true }) });

const storageTierLink = (page: Page, name: string) =>
  storageTierTable(page).getByRole('link', { name, exact: true });

const storageTierRow = (page: Page, name: string) =>
  storageTierTable(page)
    .getByRole('row')
    .filter({ has: page.getByRole('link', { name, exact: true }) });

const storageTierEmptyState = (page: Page) =>
  page.getByText('No storage tiers yet. Create one to get started.', { exact: true });

const waitForStorageTierList = async (page: Page): Promise<void> => {
  await expect
    .poll(
      async () =>
        (await storageTierTable(page).count()) > 0 ||
        (await storageTierEmptyState(page).count()) > 0,
      { timeout: 15_000, message: 'Storage tier list did not finish loading.' },
    )
    .toBe(true);
};

/** Verify the configured backend is Ready and uses the expected provider. */
export const expectStorageBackendReady = async (
  page: Page,
  backendName: string,
  provider: string,
): Promise<void> => {
  await openStorageBackendsPage(page);
  try {
    const row = storageBackendRow(page, backendName);
    await expect(row.getByText('Ready', { exact: true })).toBeVisible();
    await expect(row.getByText(provider, { exact: true })).toBeVisible();
  } catch {
    throw new Error('Configured Storage backend is not Ready with the expected provider.');
  }
};

/** Verify that a ready backend is selectable in the Storage tier create form. */
export const expectStorageBackendSelectable = async (
  page: Page,
  backendName: string,
): Promise<void> => {
  try {
    await page.locator(...StorageTierCreatePage.backend).click();
    await expect(page.getByRole('option', { name: backendName, exact: true })).toBeVisible();
    await page.keyboard.press('Escape');
  } catch {
    throw new Error('Configured Storage backend is not ready and selectable in the create form.');
  }
};

/** Select Tiers and verify that the named tier appears in the table. */
export const expectStorageTierListed = async (
  page: Page,
  tierName: string,
  timeout = 15_000,
): Promise<void> => {
  await waitForStorageTierList(page);
  try {
    await expect(storageTierLink(page, tierName)).toBeVisible({ timeout });
  } catch {
    throw new Error('The configured Storage tier was not found in the Tiers table.');
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
  await openStorageTiersPage(page);
  await waitForStorageTierList(page);
  let matchingRows: number;
  try {
    matchingRows = await storageTierLink(page, tierName).count();
  } catch {
    throw new Error('Could not verify Storage tier name availability.');
  }
  if (matchingRows > 0) {
    throw new Error('The configured Storage tier name is already in use.');
  }
};

/** Open the tier creation form and verify its title and required inputs. */
export const openCreateStorageTierPage = async (page: Page): Promise<void> => {
  const createTier = page.getByRole(...StorageTiersPage.createTier);
  await expect(createTier).toBeVisible();
  await createTier.click();

  await expect(page.getByRole(...StorageTierCreatePage.pageTitle)).toBeVisible();
  await expect(page.locator(...StorageTierCreatePage.name)).toBeVisible();
  await expect(page.locator(...StorageTierCreatePage.description)).toBeVisible();
  await expect(page.locator(...StorageTierCreatePage.backend)).toBeVisible();
  const protocolGroup = page.locator(...StorageTierCreatePage.protocolGroup);
  await expect(protocolGroup.getByRole(...StorageTierCreatePage.nfsProtocol)).toBeVisible();
  await expect(protocolGroup.getByRole(...StorageTierCreatePage.blockProtocol)).toBeVisible();
};

/** Create a storage tier using an already-existing backend. */
export const createStorageTier = async (
  page: Page,
  tier: StorageTierCreateParams,
): Promise<void> => {
  try {
    await page.locator(...StorageTierCreatePage.name).fill(tier.name);
    await page.locator(...StorageTierCreatePage.description).fill(tier.description);
    await page.locator(...StorageTierCreatePage.backend).click();
    await page.getByRole('option', { name: tier.backend, exact: true }).click();
    const protocolGroup = page.locator(...StorageTierCreatePage.protocolGroup);
    if (tier.protocol === 'NFS') {
      await protocolGroup.getByRole(...StorageTierCreatePage.nfsProtocol).check();
    } else {
      await protocolGroup.getByRole(...StorageTierCreatePage.blockProtocol).check();
    }
    await page.getByRole(...StorageTierCreatePage.create).click();
    await expect(page.getByRole('heading', { name: tier.name, exact: true })).toBeVisible();
  } catch {
    throw new Error(
      'Storage tier creation did not complete; check the configured values and backend readiness.',
    );
  }
};

/** Verify the selected backend and protocol in a tier's table row. */
export const expectStorageTierConfiguration = async (
  page: Page,
  tier: StorageTierCreateParams,
): Promise<void> => {
  try {
    const row = storageTierRow(page, tier.name);
    await expect(row.getByText(tier.backend, { exact: true })).toBeVisible();
    await expect(row.getByText(tier.protocol, { exact: true })).toBeVisible();
  } catch {
    throw new Error(
      'Storage tier configuration does not match the configured backend and protocol.',
    );
  }
};

/** Ensure the configured tier exists before a standalone delete run. */
export const ensureStorageTierExists = async (
  page: Page,
  tier: StorageTierCreateParams,
): Promise<void> => {
  await waitForStorageTierList(page);
  const existingTiers = await storageTierLink(page, tier.name).count();
  if (existingTiers > 1) {
    throw new Error('Multiple Storage tiers match the configured name.');
  }
  if (existingTiers === 1) {
    return;
  }

  await expectStorageBackendReady(page, tier.backend, 'lvms');
  await openStorageTiersPage(page);
  await openCreateStorageTierPage(page);
  await expectStorageBackendSelectable(page, tier.backend);
  await createStorageTier(page, tier);
  await openStorageTierListPage(page);
  await expectStorageTierListed(page, tier.name);
  await expectStorageTierConfiguration(page, tier);
};

const deleteStorageTierFromList = async (page: Page, tierName: string): Promise<void> => {
  const row = storageTierRow(page, tierName);
  const actionsButton = row.getByRole('button', {
    name: 'Actions for ' + tierName,
    exact: true,
  });
  try {
    await expect(actionsButton).toBeVisible({ timeout: 3_000 });
    await actionsButton.click();
  } catch {
    throw new Error('Could not open the Storage tier actions menu.');
  }
  try {
    await page.getByRole(...StorageTierDeletePage.deleteMenuItem).click();
  } catch {
    throw new Error('Could not select Storage tier deletion.');
  }

  const deleteDialog = page.locator(...StorageTierDeletePage.deleteDialog);
  try {
    await confirmRowDeletion(
      row,
      deleteDialog,
      deleteDialog.locator(...StorageTierDeletePage.deleteTitle),
      deleteDialog.getByText(...StorageTierDeletePage.deleteWarning),
      deleteDialog.getByRole(...StorageTierDeletePage.confirmDelete),
      tierName,
      'Storage tier',
      180_000,
      row.getByRole('gridcell').nth(1),
    );
  } catch {
    throw new Error('Storage tier deletion did not complete.');
  }
};

/** Open the named tier's row menu, confirm deletion, and verify it disappears. */
export const deleteStorageTier = async (page: Page, tierName: string): Promise<void> => {
  await expectStorageTierListed(page, tierName, 3_000);
  await deleteStorageTierFromList(page, tierName);
};
