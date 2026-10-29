import { expect, test } from '@playwright/test';
import { access, rm } from 'node:fs/promises';

import { catalogOwnershipFilePath, catalogTestData } from './test_data';
import { catalogItemResource, getCatalogItems } from '../../../../libs/osac-cli/src/catalog';
import { expectOsacCommandSucceeded, runOsacCommand } from '../../../../libs/osac-cli/src/cli';
import { expectCatalogItemNotListed } from '../../../../libs/test-utils/src/catalog.page';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
import { openCatalogPage } from '../../../../libs/test-utils/src/main.page';

test('deletes a catalog item with the osac CLI and removes it from Catalog', async ({ page }) => {
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

  // Confirm that the catalog item created by the preceding flow is present.
  const existingItems = await testStep(
    'catalog-delete',
    `Verify catalog item "${catalogTestData.name}" exists before deletion`,
    () => getCatalogItems(catalogTestData.name),
  );
  if (existingItems.length !== 1) {
    throw new Error(
      `Expected exactly one catalog item named '${catalogTestData.name}' before deletion, ` +
        `but found ${existingItems.length}. Run create.catalog.spec.ts first.`,
    );
  }

  // Delete the catalog item by its name.
  await testStep('catalog-delete', 'Delete catalog item with the osac CLI', async () => {
    const deleteResult = await runOsacCommand([
      'delete',
      catalogItemResource,
      catalogTestData.name,
    ]);
    expectOsacCommandSucceeded(deleteResult, `Delete catalog item '${catalogTestData.name}'`);
  });

  // Verify that the deleted catalog item can no longer be found.
  await testStep(
    'catalog-delete',
    `Verify catalog item "${catalogTestData.name}" does not exist after deletion`,
    async () => {
      const remainingItems = await getCatalogItems(catalogTestData.name);
      expect(remainingItems).toHaveLength(0);
    },
  );

  // Open the browser, authenticate, and verify the deleted item is absent from the UI.
  await testStep(
    'catalog-delete',
    `Verify catalog item "${catalogTestData.name}" is absent from the UI`,
    async () => {
      await openAndLogin(
        page,
        '/',
        process.env.OSAC_ADMIN_USERNAME ?? '',
        process.env.OSAC_ADMIN_PASSWORD ?? '',
        'admin',
      );
      await openCatalogPage(page);
      await expectCatalogItemNotListed(page, catalogTestData.name);
    },
  );

  await testStep('catalog-delete', 'Remove catalog ownership marker', () =>
    rm(ownershipFile, { force: true }),
  );
});
