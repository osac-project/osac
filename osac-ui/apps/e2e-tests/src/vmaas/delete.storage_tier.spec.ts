import { test } from '@playwright/test';

import { openAndLoginWithEnvironment } from '../../../../libs/test-utils/src/general/open_and_login';
import { openStoragePage } from '../../../../libs/test-utils/src/main.page';
import { deleteStorageTier } from '../../../../libs/test-utils/src/storage.page';

test('deletes the requested storage tier', async ({ page }) => {
  // Allow the backend deletion wait and UI actions to complete.
  test.setTimeout(180_000);

  const tierName = process.env.OSAC_STORAGE_TIER_NAME;
  if (!tierName) {
    test.skip(true, 'OSAC_STORAGE_TIER_NAME is not set.');
    return;
  }

  // 1. Open the browser and log in.
  await openAndLoginWithEnvironment(page, '/');

  // 2. Open Infrastructure > Storage, select Tiers, and delete the requested tier.
  await openStoragePage(page);
  await deleteStorageTier(page, tierName);
});
