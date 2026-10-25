import { test } from '@playwright/test';

import { storageTierNameOrSkip, storageTierTestData } from './test_data';
import { openAndLoginWithEnvironment } from '../../../../libs/test-utils/src/general/open_and_login';
import {
  createStorageTier,
  expectStorageBackendReady,
  expectStorageBackendSelectable,
  expectStorageTierConfiguration,
  expectStorageTierListed,
  expectStorageTierNotListed,
  openCreateStorageTierPage,
  openStorageTierListPage,
} from '../../../../libs/test-utils/src/storage.page';

test('creates a storage tier from Storage > Storage Tiers', async ({ page }) => {
  test.setTimeout(120_000);
  const storageTierName = storageTierNameOrSkip();
  if (!storageTierName) {
    return;
  }
  const storageTier = { ...storageTierTestData, name: storageTierName };

  // 1. Open the browser and log in.
  await openAndLoginWithEnvironment(page, '/');

  // 2. Verify the configured backend is Ready and provided by LVMS.
  await expectStorageBackendReady(page, storageTierTestData.backend, 'lvms');

  // 3. Verify the run-specific tier name is available before creating it.
  await expectStorageTierNotListed(page, storageTier.name);

  // 4. Open the create form and verify the configured backend is selectable.
  await openCreateStorageTierPage(page);
  await expectStorageBackendSelectable(page, storageTierTestData.backend);

  // 5. Create a tier using the configured values and existing backend.
  await createStorageTier(page, storageTier);

  // 6. Return to the tier list and verify the new tier appears.
  await openStorageTierListPage(page);
  await expectStorageTierListed(page, storageTier.name);
  await expectStorageTierConfiguration(page, storageTier);
});
