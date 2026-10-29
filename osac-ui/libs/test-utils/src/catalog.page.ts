import { type Locator, type Page, expect } from '@playwright/test';

import { CatalogMainPage } from '../../locators/catalog';

const getCatalogItemCard = (page: Page, name: string): Locator =>
  page.locator(...CatalogMainPage.catalogItemCards).filter({
    has: page.getByRole('link', { name, exact: true }),
  });

export interface ExpectedCatalogItemResources {
  vcpus: number;
  memoryGib: number;
  storageGib: number;
  diskImage: string;
}

/** Verify that a catalog item is shown with its expected card values. */
export const expectCatalogItemListed = async (
  page: Page,
  name: string,
  expectedType: string,
  expectedStatus: string,
  expectedUsage: string,
  expectedResources: ExpectedCatalogItemResources,
): Promise<void> => {
  const catalogItemCard = getCatalogItemCard(page, name);

  await expect(catalogItemCard).toHaveCount(1);
  await expect(catalogItemCard.getByRole('link', { name, exact: true })).toBeVisible();
  await expect(catalogItemCard.locator(...CatalogMainPage.catalogItemLabels)).toHaveText([
    expectedType,
    expectedStatus,
  ]);
  await expect(catalogItemCard.locator(...CatalogMainPage.catalogItemVisibility)).toHaveText(
    expectedUsage,
  );

  const expectedResourceValues = [
    ['vCPU', `${expectedResources.vcpus} vCPU`],
    ['Memory', `${expectedResources.memoryGib} GiB`],
    ['Storage', `${expectedResources.storageGib} GiB`],
    ['Disk image', expectedResources.diskImage],
  ] as const;

  for (const [label, value] of expectedResourceValues) {
    const resourceGroup = catalogItemCard
      .locator(...CatalogMainPage.catalogItemResourceGroups)
      .filter({ hasText: label });

    await expect(resourceGroup).toHaveCount(1);
    await expect(resourceGroup.locator(...CatalogMainPage.catalogItemResourceValue)).toHaveText(
      value,
    );
  }
};

/** Verify that a catalog item is absent from the Catalog page. */
export const expectCatalogItemNotListed = async (page: Page, name: string): Promise<void> => {
  await expect(page.getByRole(...CatalogMainPage.catalogLoadingIndicator)).toHaveCount(0);
  await expect(getCatalogItemCard(page, name)).toHaveCount(0);
};
