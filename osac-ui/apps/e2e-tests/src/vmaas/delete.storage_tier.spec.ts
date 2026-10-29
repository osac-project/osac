import { test } from '@playwright/test';

import { storageTierNameOrSkip, storageTierTestData } from './test_data';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
import { openStorageTiersPage } from '../../../../libs/test-utils/src/main.page';
import {
  deleteStorageTier,
  ensureStorageTierExists,
  expectStorageTierNotListed,
} from '../../../../libs/test-utils/src/storage.page';

test('deletes the requested storage tier', async ({ page }) => {
  // Allow creation when needed, the deletion wait, and UI actions to complete.
  test.setTimeout(420_000);

  const storageTierName = storageTierNameOrSkip();
  if (!storageTierName) {
    logProgress('storage-tier-delete', 'Skipped: storage_tier.name is not configured.');
    return;
  }
  logProgress('storage-tier-delete', `Starting flow for "${storageTierName}".`);
  const storageTier = { ...storageTierTestData, name: storageTierName };

  await testStep('storage-tier-delete', 'Log in as the admin test user', () =>
    openAndLogin(
      page,
      '/',
      process.env.OSAC_ADMIN_USERNAME ?? '',
      process.env.OSAC_ADMIN_PASSWORD ?? '',
      'admin',
    ),
  );

  await testStep('storage-tier-delete', 'Open Storage > Storage Tiers', async () => {
    await openStorageTiersPage(page);
  });

  await testStep(
    'storage-tier-delete',
    `Verify storage tier "${storageTierName}" exists before deletion`,
    async () => {
      await ensureStorageTierExists(page, storageTier);
    },
  );

  await testStep('storage-tier-delete', `Delete storage tier "${storageTierName}"`, () =>
    deleteStorageTier(page, storageTierName),
  );

  await testStep(
    'storage-tier-delete',
    `Verify storage tier "${storageTierName}" does not exist after deletion`,
    () => expectStorageTierNotListed(page, storageTierName),
  );
});
