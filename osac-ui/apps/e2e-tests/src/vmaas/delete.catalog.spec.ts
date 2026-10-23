import { expect, test } from '@playwright/test';

import { catalogTestData } from './test_data';
import { catalogItemResource, getCatalogItems } from '../../../../libs/osac-cli/src/catalog';
import { expectOsacCommandSucceeded, runOsacCommand } from '../../../../libs/osac-cli/src/cli';
import { expectCatalogItemNotListed } from '../../../../libs/test-utils/src/catalog.page';
import { openAndLoginWithEnvironment } from '../../../../libs/test-utils/src/general/open_and_login';
import { openCatalogPage } from '../../../../libs/test-utils/src/main.page';

test('deletes a catalog item with the osac CLI and removes it from Catalog', async ({ page }) => {
  test.setTimeout(180_000);

  // Confirm that the catalog item created by the preceding flow is present.
  const existingItems = await getCatalogItems(catalogTestData.name);
  if (existingItems.length !== 1) {
    throw new Error(
      `Expected exactly one catalog item named '${catalogTestData.name}' before deletion, ` +
        `but found ${existingItems.length}. Run create.catalog.spec.ts first.`,
    );
  }

  // Delete the catalog item by its name.
  const deleteResult = await runOsacCommand(['delete', catalogItemResource, catalogTestData.name]);
  expectOsacCommandSucceeded(deleteResult, `Delete catalog item '${catalogTestData.name}'`);

  // Verify that the deleted catalog item can no longer be found.
  const remainingItems = await getCatalogItems(catalogTestData.name);
  expect(remainingItems).toHaveLength(0);

  // Open the browser, authenticate, and verify the deleted item is absent from the UI.
  await openAndLoginWithEnvironment(page, '/');
  await openCatalogPage(page);
  await expectCatalogItemNotListed(page, catalogTestData.name);
});
