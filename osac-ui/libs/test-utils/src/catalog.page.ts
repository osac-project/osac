import { type Locator, type Page, expect } from '@playwright/test';

import { confirmDetailDeletion } from './general/row_assertions';
import { CatalogCreatePage, CatalogDeletePage, CatalogMainPage } from '../../locators/catalog';

const getCatalogItemCard = (page: Page, name: string): Locator =>
  page.locator(...CatalogMainPage.catalogItemCards).filter({
    has: page.getByRole('link', { name, exact: true }),
  });

/**
 * The Catalog page groups items by resource type and defaults to the first
 * enabled tier, so VM items and the VM create action are only shown after
 * selecting the Virtual Machines type filter.
 */
const selectVmCatalogTab = async (page: Page): Promise<void> => {
  const tab = page.locator(...CatalogMainPage.vmTypeFilterTab);
  if ((await tab.getAttribute('aria-pressed')) !== 'true') {
    await tab.click();
  }
};

export interface ExpectedCatalogItemResources {
  instanceType: string;
  diskImage: string;
}

/** Open the VM catalog item creation wizard. */
export const openCreateCatalogItemPage = async (page: Page): Promise<void> => {
  await selectVmCatalogTab(page);
  await page.getByRole(...CatalogMainPage.createVmCatalogItem).click();
  await expect(page).toHaveURL((url) => url.pathname === '/catalog/vm/create');
  await expect(page.getByRole(...CatalogCreatePage.pageTitle)).toBeVisible();
};

/** Verify that a catalog item is shown with its expected card values. */
export const expectCatalogItemListed = async (
  page: Page,
  name: string,
  expectedStatus: string,
  expectedUsage: string,
  expectedResources: ExpectedCatalogItemResources,
): Promise<void> => {
  await selectVmCatalogTab(page);
  const catalogItemCard = getCatalogItemCard(page, name);

  await expect(catalogItemCard).toHaveCount(1);
  await expect(catalogItemCard.getByRole('link', { name, exact: true })).toBeVisible();
  await expect(catalogItemCard.locator(...CatalogMainPage.catalogItemLabels)).toHaveText(
    expectedStatus,
  );
  await expect(catalogItemCard.locator(...CatalogMainPage.catalogItemVisibility)).toHaveText(
    expectedUsage,
  );

  const expectedResourceValues = [
    ['Instance type', expectedResources.instanceType],
    ['Disk image', expectedResources.diskImage],
  ] as const;

  for (const [label, value] of expectedResourceValues) {
    const resourceGroup = catalogItemCard
      .locator(...CatalogMainPage.catalogItemResourceGroups)
      .filter({ hasText: label });

    await expect(resourceGroup).toHaveCount(1);
    await expect(
      resourceGroup
        .locator(...CatalogMainPage.catalogItemResourceValue)
        .getByText(value, { exact: true }),
    ).toHaveText(value);
  }

  await expect(catalogItemCard.getByText('Storage', { exact: true })).toHaveCount(0);
};

/** Verify that a catalog item is absent from the Catalog page. */
export const expectCatalogItemNotListed = async (page: Page, name: string): Promise<void> => {
  await selectVmCatalogTab(page);
  await expect(page.getByRole(...CatalogMainPage.catalogLoadingIndicator)).toHaveCount(0);
  await expect(getCatalogItemCard(page, name)).toHaveCount(0);
};

/** Open a published catalog item from the tenant Catalog page. */
export const openCatalogItemDetails = async (page: Page, name: string): Promise<void> => {
  await selectVmCatalogTab(page);
  const catalogItemCard = getCatalogItemCard(page, name);

  await expect(catalogItemCard).toHaveCount(1);
  await expect(catalogItemCard.getByRole('link', { name, exact: true })).toBeVisible();
  await catalogItemCard.getByRole('link', { name, exact: true }).click();
  await expect(page.getByRole('heading', { name, exact: true })).toBeVisible();
};

/** Delete a catalog item through its UI details page. */
export const deleteCatalogItem = async (page: Page, name: string): Promise<void> => {
  await openCatalogItemDetails(page, name);
  await page.getByRole(...CatalogDeletePage.delete).click();

  const dialog = page.locator(...CatalogDeletePage.dialog);
  await confirmDetailDeletion(
    dialog,
    dialog.locator(...CatalogDeletePage.dialogTitle),
    dialog.getByText(...CatalogDeletePage.warning),
    dialog.getByRole(...CatalogDeletePage.delete),
    name,
  );
  await expect(page).toHaveURL((url) => url.pathname === '/catalog/vms');
  await expect(page.getByRole(...CatalogMainPage.pageTitle)).toBeVisible();
};

/** Launch the published VM catalog item from its details page. */
export const launchCatalogItem = async (page: Page, name: string): Promise<string> => {
  const itemPath = new URL(page.url()).pathname;
  const match = /^\/catalog\/vms\/([^/]+)$/.exec(itemPath);
  if (!match) {
    throw new Error(`Expected a VM catalog item details page, got '${itemPath}'.`);
  }
  const launchButton = page.getByRole('button', { name: 'Launch instance', exact: true });

  await expect(launchButton).toBeVisible();
  await expect(launchButton).toBeEnabled();
  await launchButton.click();
  await expect(page).toHaveURL((url) => url.pathname === `${itemPath}/launch`);
  await expect(
    page.getByRole('heading', { name: 'Create a virtual machine', exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name, exact: true }),
  ).toHaveAttribute('href', itemPath);
  return decodeURIComponent(match[1]);
};
