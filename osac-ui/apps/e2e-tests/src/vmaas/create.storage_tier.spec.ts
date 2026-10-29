import { test } from '@playwright/test';

import { storageTierNameOrSkip, storageTierTestData } from './test_data';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
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
    logProgress('storage-tier-create', 'Skipped: storage_tier.name is not configured.');
    return;
  }
  logProgress('storage-tier-create', `Starting flow for "${storageTierName}".`);
  const storageTier = { ...storageTierTestData, name: storageTierName };

  await testStep('storage-tier-create', 'Log in as the admin test user', () =>
    openAndLogin(
      page,
      '/',
      process.env.OSAC_ADMIN_USERNAME ?? '',
      process.env.OSAC_ADMIN_PASSWORD ?? '',
      'admin',
    ),
  );

  await testStep('storage-tier-create', 'Verify the LVMS backend is ready', () =>
    expectStorageBackendReady(page, storageTierTestData.backend, 'lvms'),
  );

  await testStep(
    'storage-tier-create',
    `Verify storage tier "${storageTier.name}" does not exist before creation`,
    () => expectStorageTierNotListed(page, storageTier.name),
  );

  await testStep('storage-tier-create', 'Open the storage tier creation form', async () => {
    await openCreateStorageTierPage(page);
    await expectStorageBackendSelectable(page, storageTierTestData.backend);
  });

  await testStep('storage-tier-create', 'Create the configured storage tier', () =>
    createStorageTier(page, storageTier),
  );

  await testStep(
    'storage-tier-create',
    `Verify storage tier "${storageTier.name}" exists after creation`,
    async () => {
      await openStorageTierListPage(page);
      await expectStorageTierListed(page, storageTier.name);
      await expectStorageTierConfiguration(page, storageTier);
    },
  );
});
