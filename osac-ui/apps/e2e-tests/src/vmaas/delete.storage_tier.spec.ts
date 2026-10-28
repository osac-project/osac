import { test } from '@playwright/test';

import { storageTierNameOrSkip, storageTierTestData } from './test_data';
import { openAndLoginWithEnvironment } from '../../../../libs/test-utils/src/general/open_and_login';
import { openStorageTiersPage } from '../../../../libs/test-utils/src/main.page';
import {
  deleteStorageTier,
  ensureStorageTierExists,
} from '../../../../libs/test-utils/src/storage.page';

test('deletes the requested storage tier', async ({ page }) => {
  // Allow creation when needed, the deletion wait, and UI actions to complete.
  test.setTimeout(420_000);

  const storageTierName = storageTierNameOrSkip();
  if (!storageTierName) {
    return;
  }
  const storageTier = { ...storageTierTestData, name: storageTierName };

  // 1. Open the browser and log in.
  await openAndLoginWithEnvironment(page, '/');

  // 2. Open Storage > Storage Tiers and ensure the requested tier exists.
  await openStorageTiersPage(page);
  await ensureStorageTierExists(page, storageTier);

  // 3. Delete the requested tier.
  await deleteStorageTier(page, storageTierName);
});
