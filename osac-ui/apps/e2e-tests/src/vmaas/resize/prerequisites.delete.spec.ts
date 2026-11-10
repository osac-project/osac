import { test } from '@playwright/test';
import { rm } from 'node:fs/promises';

import {
  deleteCatalogFlow,
  deleteDiskImageFlow,
  deleteInstanceTypeFlow,
  deleteStorageTierFlow,
  deleteTenantFlow,
} from '../delete_flows';
import {
  type ResizeOwnedResource,
  isResizeResourceOwned,
  removeResizeOwnershipMarker,
} from './ownership';
import { openAndLogin } from '../../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../../libs/test-utils/src/general/test_progress';
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

const deleteOwnedResource = async (
  resource: ResizeOwnedResource,
  label: string,
  deleteResource: () => Promise<void>,
): Promise<void> => {
  if (!(await isResizeResourceOwned(resource))) {
    logProgress('resize-cleanup', `${label} is not owned by this run; leaving it untouched.`);
    return;
  }
  await deleteResource();
  await removeResizeOwnershipMarker(resource);
};

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

  await testStep('resize-cleanup', 'Delete the resize catalog item', () =>
    deleteOwnedResource('catalog', 'Catalog', async () => {
      await deleteCatalogFlow(page, catalogTestData.name);
      await rm(catalogOwnershipFilePath(catalogTestData.name), { force: true });
    }),
  );

  await testStep('resize-cleanup', `Delete instance type "${targetInstanceTypeName}"`, () =>
    deleteOwnedResource('instance-type-target', 'Target instance type', () =>
      deleteInstanceTypeFlow(
        page,
        targetInstanceTypeName,
        resizeInstanceTypeTestData.description,
        resizeInstanceTypeTestData.vcpus,
        resizeInstanceTypeTestData.memoryGib,
      ),
    ),
  );

  await testStep('resize-cleanup', `Delete instance type "${baseInstanceTypeName}"`, () =>
    deleteOwnedResource('instance-type-base', 'Base instance type', () =>
      deleteInstanceTypeFlow(
        page,
        baseInstanceTypeName,
        instanceTypeTestData.description,
        instanceTypeTestData.vcpus,
        instanceTypeTestData.memoryGib,
        instanceTypeTestData.gpu,
      ),
    ),
  );

  await testStep('resize-cleanup', `Delete storage tier "${storageTierName}"`, () =>
    deleteOwnedResource('storage-tier', 'Storage tier', () =>
      deleteStorageTierFlow(page, { ...storageTierTestData, name: storageTierName }),
    ),
  );

  await testStep('resize-cleanup', `Delete disk image "${diskImageName}"`, () =>
    deleteOwnedResource('disk-image', 'Disk image', () =>
      deleteDiskImageFlow(page, { ...diskImagesTestData, name: diskImageName }),
    ),
  );

  await testStep('resize-cleanup', `Delete tenant "${tenantName}"`, () =>
    deleteOwnedResource('tenant', 'Tenant', () => deleteTenantFlow(page, tenantName, '')),
  );
});
