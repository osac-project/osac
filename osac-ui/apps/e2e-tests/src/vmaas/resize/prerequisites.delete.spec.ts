import { test } from '@playwright/test';
import { rm } from 'node:fs/promises';

import {
  deleteCatalogFlow,
  deleteDiskImageFlow,
  deleteInstanceTypeFlow,
  deleteStorageTierFlow,
  deleteTenantFlow,
} from '../delete_flows';
import { isResizeResourceOwned, removeResizeOwnershipMarker } from './ownership';
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
    if (!(await isResizeResourceOwned('catalog'))) {
      logProgress('resize-cleanup', 'Catalog is not owned by this run; leaving it untouched.');
      return;
    }
    await deleteCatalogFlow(page, catalogTestData.name);
    await rm(catalogOwnershipFilePath(catalogTestData.name), { force: true });
    await removeResizeOwnershipMarker('catalog');
  });

  await testStep('resize-cleanup', `Delete instance type "${targetInstanceTypeName}"`, async () => {
    if (!(await isResizeResourceOwned('instance-type-target'))) {
      logProgress(
        'resize-cleanup',
        'Target instance type is not owned by this run; leaving it untouched.',
      );
      return;
    }
    await deleteInstanceTypeFlow(
      page,
      targetInstanceTypeName,
      resizeInstanceTypeTestData.description,
      resizeInstanceTypeTestData.vcpus,
      resizeInstanceTypeTestData.memoryGib,
    );
    await removeResizeOwnershipMarker('instance-type-target');
  });

  await testStep('resize-cleanup', `Delete instance type "${baseInstanceTypeName}"`, async () => {
    if (!(await isResizeResourceOwned('instance-type-base'))) {
      logProgress(
        'resize-cleanup',
        'Base instance type is not owned by this run; leaving it untouched.',
      );
      return;
    }
    await deleteInstanceTypeFlow(
      page,
      baseInstanceTypeName,
      instanceTypeTestData.description,
      instanceTypeTestData.vcpus,
      instanceTypeTestData.memoryGib,
      instanceTypeTestData.gpu,
    );
    await removeResizeOwnershipMarker('instance-type-base');
  });

  await testStep('resize-cleanup', `Delete storage tier "${storageTierName}"`, async () => {
    if (!(await isResizeResourceOwned('storage-tier'))) {
      logProgress('resize-cleanup', 'Storage tier is not owned by this run; leaving it untouched.');
      return;
    }
    await deleteStorageTierFlow(page, { ...storageTierTestData, name: storageTierName });
    await removeResizeOwnershipMarker('storage-tier');
  });

  await testStep('resize-cleanup', `Delete disk image "${diskImageName}"`, async () => {
    if (!(await isResizeResourceOwned('disk-image'))) {
      logProgress('resize-cleanup', 'Disk image is not owned by this run; leaving it untouched.');
      return;
    }
    await deleteDiskImageFlow(page, { ...diskImagesTestData, name: diskImageName });
    await removeResizeOwnershipMarker('disk-image');
  });

  await testStep('resize-cleanup', `Delete tenant "${tenantName}"`, async () => {
    if (!(await isResizeResourceOwned('tenant'))) {
      logProgress('resize-cleanup', 'Tenant is not owned by this run; leaving it untouched.');
      return;
    }
    await deleteTenantFlow(page, tenantName, '');
    await removeResizeOwnershipMarker('tenant');
  });
});
