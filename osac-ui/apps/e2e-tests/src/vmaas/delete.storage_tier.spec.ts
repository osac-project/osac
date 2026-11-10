import { test } from '@playwright/test';

import { deleteStorageTierFlow } from './delete_flows';
import { storageTierNameOrSkip, storageTierTestData } from './test_data';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';

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

  await testStep('storage-tier-delete', `Delete storage tier "${storageTierName}"`, () =>
    deleteStorageTierFlow(page, storageTier),
  );
});
