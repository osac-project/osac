import { test } from '@playwright/test';

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
  catalogTestData,
  diskImageNameOrSkip,
  diskImagesTestData,
  instanceTypeNameOrSkip,
  instanceTypeTestData,
  resizeInstanceTypeNameOrSkip,
  resizeInstanceTypeTestData,
  resizeMixedInstanceTypeNameOrSkip,
  resizeMixedInstanceTypeTestData,
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
  const mixedInstanceTypeName = resizeMixedInstanceTypeNameOrSkip();
  const baseInstanceTypeName = instanceTypeNameOrSkip();
  const storageTierName = storageTierNameOrSkip();
  const diskImageName = diskImageNameOrSkip();
  const tenantName = tenantNameOrSkip();
  if (
    !targetInstanceTypeName ||
    !mixedInstanceTypeName ||
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
    }),
  );

  const instanceTypesToDelete = [
    {
      name: targetInstanceTypeName,
      resource: 'instance-type-target',
      label: 'Target instance type',
      description: resizeInstanceTypeTestData.description,
      vcpus: resizeInstanceTypeTestData.vcpus,
      memoryGib: resizeInstanceTypeTestData.memoryGib,
      gpu: undefined,
    },
    {
      name: mixedInstanceTypeName,
      resource: 'instance-type-mixed',
      label: 'Mixed-direction instance type',
      description: resizeMixedInstanceTypeTestData.description,
      vcpus: resizeMixedInstanceTypeTestData.vcpus,
      memoryGib: resizeMixedInstanceTypeTestData.memoryGib,
      gpu: undefined,
    },
    {
      name: baseInstanceTypeName,
      resource: 'instance-type-base',
      label: 'Base instance type',
      description: instanceTypeTestData.description,
      vcpus: instanceTypeTestData.vcpus,
      memoryGib: instanceTypeTestData.memoryGib,
      gpu: instanceTypeTestData.gpu,
    },
  ] as const;
  for (const instanceType of instanceTypesToDelete) {
    await testStep('resize-cleanup', `Delete instance type "${instanceType.name}"`, () =>
      deleteOwnedResource(instanceType.resource, instanceType.label, () =>
        deleteInstanceTypeFlow(
          page,
          instanceType.name,
          instanceType.description,
          instanceType.vcpus,
          instanceType.memoryGib,
          instanceType.gpu,
        ),
      ),
    );
  }

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
    deleteOwnedResource('tenant', 'Tenant', () => deleteTenantFlow(page, tenantName)),
  );
});
