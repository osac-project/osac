import { test } from '@playwright/test';

import { deleteCatalogFlow } from './delete_flows';
import { isResizeResourceOwned, removeResizeOwnershipMarker } from './resize/ownership';
import { catalogTestData } from './test_data';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';

test('deletes a catalog item through the Catalog UI', async ({ page }) => {
  test.setTimeout(180_000);
  logProgress('catalog-delete', `Starting flow for "${catalogTestData.name}".`);

  const createdByThisRun = await testStep(
    'catalog-delete',
    `Verify catalog item "${catalogTestData.name}" belongs to this run`,
    () => isResizeResourceOwned('catalog'),
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

  await testStep('catalog-delete', 'Delete the catalog item through the UI', () =>
    deleteCatalogFlow(page, catalogTestData.name),
  );

  await testStep('catalog-delete', 'Remove catalog ownership marker', () =>
    removeResizeOwnershipMarker('catalog'),
  );
});
