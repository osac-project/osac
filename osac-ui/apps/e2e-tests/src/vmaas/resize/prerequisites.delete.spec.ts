import { expect, test } from '@playwright/test';
import { access, rm } from 'node:fs/promises';

import { deleteTenantSshKey } from '../../../../../libs/osac-cli/src/secret';
import { deleteTenantAdmin } from '../../../../../libs/osac-cli/src/tenant_admin';
import {
  deleteCatalogItem,
  expectCatalogItemNotListed,
} from '../../../../../libs/test-utils/src/catalog.page';
import {
  deleteDiskImage,
  ensureDiskImageExists,
  expectDiskImageNotToExist,
  includeObsoleteDiskImages,
  openDiskImageDetails,
} from '../../../../../libs/test-utils/src/disk_images.page';
import { openAndLogin } from '../../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../../libs/test-utils/src/general/test_progress';
import {
  deleteInstanceType,
  ensureInstanceTypeExists,
  expectInstanceTypeNotToExist,
} from '../../../../../libs/test-utils/src/instance_types.page';
import {
  openCatalogPage,
  openDiskImagesPage,
  openInstanceTypesPage,
  openStorageTiersPage,
  openTenantsPage,
} from '../../../../../libs/test-utils/src/main.page';
import {
  deleteStorageTier,
  ensureStorageTierExists,
  expectStorageTierNotListed,
} from '../../../../../libs/test-utils/src/storage.page';
import {
  deleteTenant,
  expectTenantNotToExist,
  expectTenantToExist,
  readTenantCredentials,
  tenantCredentialsFilePath,
} from '../../../../../libs/test-utils/src/tenants.page';
import {
  catalogOwnershipFilePath,
  catalogTestData,
  diskImageNameOrSkip,
  diskImagesTestData,
  instanceTypeNameOrSkip,
  instanceTypeTestData,
  resizeInstanceTypeNameOrSkip,
  resizeInstanceTypeTestData,
  storageTierNameOrSkip,
  storageTierTestData,
  tenantNameOrSkip,
} from '../test_data';

test('deletes the resize prerequisites', async ({ page }) => {
  test.setTimeout(900_000);
  logProgress('resize-cleanup', 'Removing the resize scenario prerequisites.');

  const targetInstanceTypeName = resizeInstanceTypeNameOrSkip();
  const baseInstanceTypeName = instanceTypeNameOrSkip();
  const storageTierName = storageTierNameOrSkip();
  const diskImageName = diskImageNameOrSkip();
  const tenantName = tenantNameOrSkip();
  if (
    !targetInstanceTypeName ||
    !baseInstanceTypeName ||
    !storageTierName ||
    !diskImageName ||
    !tenantName
  ) {
    return;
  }

  await testStep('resize-cleanup', 'Log in as the admin test user', () =>
    openAndLogin(
      page,
      '/',
      process.env.OSAC_ADMIN_USERNAME ?? '',
      process.env.OSAC_ADMIN_PASSWORD ?? '',
      'admin',
    ),
  );

  await testStep('resize-cleanup', 'Delete the resize catalog item', async () => {
    await openCatalogPage(page);
    const catalogLink = page.getByRole('link', { name: catalogTestData.name, exact: true });
    try {
      await expect(catalogLink).toHaveCount(1, { timeout: 5_000 });
      await deleteCatalogItem(page, catalogTestData.name);
    } catch {
      await expectCatalogItemNotListed(page, catalogTestData.name);
    }
    await rm(catalogOwnershipFilePath(catalogTestData.name), { force: true });
  });

  await testStep('resize-cleanup', `Delete instance type "${targetInstanceTypeName}"`, async () => {
    await openInstanceTypesPage(page);
    await ensureInstanceTypeExists(
      page,
      targetInstanceTypeName,
      resizeInstanceTypeTestData.description,
      resizeInstanceTypeTestData.vcpus,
      resizeInstanceTypeTestData.memoryGib,
    );
    await deleteInstanceType(page, targetInstanceTypeName);
    await expectInstanceTypeNotToExist(page, targetInstanceTypeName);
  });

  await testStep('resize-cleanup', `Delete instance type "${baseInstanceTypeName}"`, async () => {
    await openInstanceTypesPage(page);
    await ensureInstanceTypeExists(
      page,
      baseInstanceTypeName,
      instanceTypeTestData.description,
      instanceTypeTestData.vcpus,
      instanceTypeTestData.memoryGib,
      instanceTypeTestData.gpu,
    );
    await deleteInstanceType(page, baseInstanceTypeName);
    await expectInstanceTypeNotToExist(page, baseInstanceTypeName);
  });

  await testStep('resize-cleanup', `Delete storage tier "${storageTierName}"`, async () => {
    await openStorageTiersPage(page);
    const storageTier = { ...storageTierTestData, name: storageTierName };
    await ensureStorageTierExists(page, storageTier);
    await deleteStorageTier(page, storageTierName);
    await expectStorageTierNotListed(page, storageTierName);
  });

  await testStep('resize-cleanup', `Delete disk image "${diskImageName}"`, async () => {
    await openDiskImagesPage(page);
    await ensureDiskImageExists(page, { ...diskImagesTestData, name: diskImageName });
    await includeObsoleteDiskImages(page);
    await openDiskImageDetails(page, diskImageName);
    await deleteDiskImage(page, diskImageName);
    await expectDiskImageNotToExist(page, diskImageName);
  });

  await testStep('resize-cleanup', `Delete tenant "${tenantName}"`, async () => {
    const credentialsFile = tenantCredentialsFilePath(tenantName);
    await openTenantsPage(page);
    await expectTenantToExist(page, tenantName);
    const tenantCredentials = await readTenantCredentials(credentialsFile);
    await deleteTenantAdmin(tenantName, tenantCredentials.username);
    await deleteTenantSshKey(tenantName);
    await deleteTenant(page, tenantName);
    await expectTenantNotToExist(page, tenantName);
    await expect(access(credentialsFile)).rejects.toThrow();
  });
});
