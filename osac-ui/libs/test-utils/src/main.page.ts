import { type Page, expect } from '@playwright/test';

import { CatalogMainPage } from '../../locators/catalog';
import { DiskImagesMainPage } from '../../locators/disk_images';
import { InstanceTypesMainPage } from '../../locators/instance_types';
import { MainPage } from '../../locators/main_page';
import { StorageBackendsPage, StorageTiersPage } from '../../locators/storage';
import { TenantsMainPage } from '../../locators/tenants';

export type MainPageOption = Exclude<keyof typeof MainPage, 'navigation'>;

type ExpandableMainPageOption = 'administration' | 'infrastructure' | 'networking' | 'storage';

const parentByNestedOption: Partial<Record<MainPageOption, ExpandableMainPageOption>> = {
  tenants: 'administration',
  storageBackends: 'storage',
  storageTiers: 'storage',
  instanceTypes: 'infrastructure',
  externalIpPools: 'infrastructure',
  diskImages: 'infrastructure',
  virtualNetworks: 'networking',
  securityGroups: 'networking',
  externalIps: 'networking',
};

const getMainPageLocator = (page: Page, option: MainPageOption) => {
  const [role, options] = MainPage[option];
  return page.getByRole(role, options);
};

const openSection = async (page: Page, option: ExpandableMainPageOption): Promise<void> => {
  const section = getMainPageLocator(page, option);
  await expect(section).toBeVisible();
  if ((await section.getAttribute('aria-expanded')) !== 'true') {
    await section.click();
    await expect(section).toHaveAttribute('aria-expanded', 'true');
  }
};

/** Open a primary-navigation option, expanding its section when necessary. */
export const openMainPageOption = async (page: Page, option: MainPageOption): Promise<void> => {
  if (
    option === 'administration' ||
    option === 'infrastructure' ||
    option === 'networking' ||
    option === 'storage'
  ) {
    await openSection(page, option);
    return;
  }

  const parent = parentByNestedOption[option];
  if (parent) {
    await openSection(page, parent);
  }

  await getMainPageLocator(page, option).click();
};

/** Open the Tenants page from the Administration section and verify its title. */
export const openTenantsPage = async (page: Page): Promise<void> => {
  await openMainPageOption(page, 'tenants');

  await expect(page.getByRole(...TenantsMainPage.pageTitle)).toBeVisible();
};

/** Open the Instance types page from the Infrastructure section and verify its title. */
export const openInstanceTypesPage = async (page: Page): Promise<void> => {
  await openMainPageOption(page, 'instanceTypes');

  await expect(page.getByRole(...InstanceTypesMainPage.pageTitle)).toBeVisible();
};

/** Open the Disk images page from the Infrastructure section and verify its title. */
export const openDiskImagesPage = async (page: Page): Promise<void> => {
  await openMainPageOption(page, 'diskImages');

  await expect(page.getByRole(...DiskImagesMainPage.pageTitle)).toBeVisible();
};

/** Open the Catalog page and verify its title. */
export const openCatalogPage = async (page: Page): Promise<void> => {
  await openMainPageOption(page, 'catalog');

  await expect(page).toHaveURL(/\/catalog(?:\?.*)?$/);
  await expect(page.getByRole(...CatalogMainPage.pageTitle)).toBeVisible();
};

/** Open Storage > Storage Backends and verify the page and navigation item. */
export const openStorageBackendsPage = async (page: Page): Promise<void> => {
  await openMainPageOption(page, 'storageBackends');

  await expect(page.getByRole(...StorageBackendsPage.pageTitle)).toBeVisible();
};

/** Open Storage > Storage Tiers and verify the page and navigation item. */
export const openStorageTiersPage = async (page: Page): Promise<void> => {
  await openMainPageOption(page, 'storageTiers');

  await expect(page.getByRole(...StorageTiersPage.pageTitle)).toBeVisible();
};
