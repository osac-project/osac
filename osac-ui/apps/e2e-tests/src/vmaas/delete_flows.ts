import { type Page, expect } from '@playwright/test';
import { access } from 'node:fs/promises';

import { deleteTenantSshKey } from '../../../../libs/osac-cli/src/secret';
import { deleteTenantAdmin } from '../../../../libs/osac-cli/src/tenant_admin';
import {
  deleteCatalogItem,
  expectCatalogItemNotListed,
} from '../../../../libs/test-utils/src/catalog.page';
import {
  deleteDiskImage,
  ensureDiskImageExists,
  expectDiskImageNotToExist,
  includeObsoleteDiskImages,
  openDiskImageDetails,
} from '../../../../libs/test-utils/src/disk_images.page';
import type { DiskImageCreateParams } from '../../../../libs/test-utils/src/disk_images.page';
import {
  deleteInstanceType,
  ensureInstanceTypeExists,
  expectInstanceTypeNotToExist,
} from '../../../../libs/test-utils/src/instance_types.page';
import type { InstanceTypeGpu } from '../../../../libs/test-utils/src/instance_types.page';
import {
  openCatalogPage,
  openDiskImagesPage,
  openInstanceTypesPage,
  openStorageTiersPage,
  openTenantsPage,
} from '../../../../libs/test-utils/src/main.page';
import {
  type StorageTierCreateParams,
  deleteStorageTier,
  ensureStorageTierExists,
  expectStorageTierNotListed,
} from '../../../../libs/test-utils/src/storage.page';
import {
  deleteTenant,
  expectTenantNotToExist,
  readTenantCredentials,
  tenantCredentialsFilePath,
} from '../../../../libs/test-utils/src/tenants.page';

export const deleteCatalogFlow = async (page: Page, name: string): Promise<void> => {
  await openCatalogPage(page);
  await deleteCatalogItem(page, name);
  await expectCatalogItemNotListed(page, name);
};

export const deleteInstanceTypeFlow = async (
  page: Page,
  name: string,
  description: string,
  vcpus: number,
  memoryGib: number,
  gpu?: InstanceTypeGpu,
): Promise<void> => {
  await openInstanceTypesPage(page);
  await ensureInstanceTypeExists(page, name, description, vcpus, memoryGib, gpu);
  await deleteInstanceType(page, name);
  await expectInstanceTypeNotToExist(page, name);
};

export const deleteStorageTierFlow = async (
  page: Page,
  storageTier: StorageTierCreateParams,
): Promise<void> => {
  await openStorageTiersPage(page);
  await ensureStorageTierExists(page, storageTier);
  await deleteStorageTier(page, storageTier.name);
  await expectStorageTierNotListed(page, storageTier.name);
};

export const deleteDiskImageFlow = async (
  page: Page,
  diskImage: DiskImageCreateParams,
): Promise<void> => {
  await openDiskImagesPage(page);
  await ensureDiskImageExists(page, diskImage);
  await includeObsoleteDiskImages(page);
  await openDiskImageDetails(page, diskImage.name);
  await deleteDiskImage(page, diskImage.name);
  await expectDiskImageNotToExist(page, diskImage.name);
};

export const deleteTenantFlow = async (page: Page, tenantName: string): Promise<void> => {
  const credentialsFile = tenantCredentialsFilePath(tenantName);
  await openTenantsPage(page);
  const tenantCredentials = await readTenantCredentials(credentialsFile);
  await deleteTenantAdmin(tenantName, tenantCredentials.username);
  await deleteTenantSshKey(tenantName);
  await deleteTenant(page, tenantName);
  await expectTenantNotToExist(page, tenantName);
  await expect(access(credentialsFile)).rejects.toThrow();
};
