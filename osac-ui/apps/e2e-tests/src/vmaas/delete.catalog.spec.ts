import { test } from '@playwright/test';
import { access, rm } from 'node:fs/promises';

import { catalogOwnershipFilePath, catalogTestData } from './test_data';
import {
  deleteCatalogItem,
  expectCatalogItemListed,
  expectCatalogItemNotListed,
} from '../../../../libs/test-utils/src/catalog.page';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
import { openCatalogPage } from '../../../../libs/test-utils/src/main.page';

test('deletes a catalog item through the Catalog UI', async ({ page }) => {
  test.setTimeout(180_000);
  logProgress('catalog-delete', `Starting flow for "${catalogTestData.name}".`);

  const ownershipFile = catalogOwnershipFilePath(catalogTestData.name);
  const createdByThisRun = await testStep(
    'catalog-delete',
    `Verify catalog item "${catalogTestData.name}" belongs to this run`,
    async () => {
      try {
        await access(ownershipFile);
        return true;
      } catch (error) {
        if ((error as NodeJS.ErrnoException).code === 'ENOENT') {
          return false;
        }
        throw error;
      }
    },
  );
  if (!createdByThisRun) {
    test.skip(
      true,
      `Catalog item "${catalogTestData.name}" was not created by this run; leaving it untouched.`,
    );
    return;
  }

  await testStep('catalog-delete', 'Log in as the admin test user', () =>
    openAndLogin(
      page,
      '/',
      process.env.OSAC_ADMIN_USERNAME ?? '',
      process.env.OSAC_ADMIN_PASSWORD ?? '',
      'admin',
    ),
  );

  await testStep('catalog-delete', 'Open the Catalog page', () => openCatalogPage(page));

  await testStep(
    'catalog-delete',
    `Verify catalog item "${catalogTestData.name}" exists before deletion`,
    () =>
      expectCatalogItemListed(
        page,
        catalogTestData.name,
        catalogTestData.type,
        catalogTestData.status,
        catalogTestData.usage,
        {
          instanceType: catalogTestData.fields.instanceType.name,
          diskImage: catalogTestData.fields.diskImage.name,
        },
      ),
  );

  await testStep('catalog-delete', 'Delete the catalog item through the UI', () =>
    deleteCatalogItem(page, catalogTestData.name),
  );

  await testStep(
    'catalog-delete',
    `Verify catalog item "${catalogTestData.name}" is absent from the UI`,
    () => expectCatalogItemNotListed(page, catalogTestData.name),
  );

  await testStep('catalog-delete', 'Remove catalog ownership marker', () =>
    rm(ownershipFile, { force: true }),
  );
});
