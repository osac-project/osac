import { test } from '@playwright/test';

import { openAndLoginWithEnvironment } from '../../../../libs/test-utils/src/general/open_and_login';
import { openStoragePage } from '../../../../libs/test-utils/src/main.page';
import {
  type StorageTierCreateParams,
  type StorageTierProtocol,
  createStorageTier,
  expectStorageBackendListed,
  expectStorageTierConfiguration,
  expectStorageTierListed,
  expectStorageTierNotListed,
  localStorageTier,
  openCreateStorageTierPage,
  openStorageTierListPage,
} from '../../../../libs/test-utils/src/storage.page';

const protocolFromEnvironment = (): StorageTierProtocol => {
  const protocol = process.env.OSAC_STORAGE_TIER_PROTOCOL ?? localStorageTier.protocol;
  if (protocol !== 'NFS' && protocol !== 'Block') {
    throw new Error('OSAC_STORAGE_TIER_PROTOCOL must be either NFS or Block.');
  }
  return protocol;
};

test('creates a storage tier from Infrastructure > Storage', async ({ page }) => {
  test.setTimeout(120_000);
  const tierName = process.env.OSAC_STORAGE_TIER_NAME;
  if (!tierName) {
    test.skip(true, 'OSAC_STORAGE_TIER_NAME is not set.');
    return;
  }

  const tier: StorageTierCreateParams = {
    ...localStorageTier,
    name: tierName,
    description: process.env.OSAC_STORAGE_TIER_DESCRIPTION ?? localStorageTier.description,
    backend: process.env.OSAC_STORAGE_TIER_BACKEND ?? localStorageTier.backend,
    protocol: protocolFromEnvironment(),
  };

  // 1. Open the browser and log in.
  await openAndLoginWithEnvironment(page, '/');

  // 2. Open Infrastructure > Storage and verify the existing local backend.
  await openStoragePage(page);
  await expectStorageBackendListed(page, 'local');

  // 3. Verify the existing local tier before creating another tier.
  await expectStorageTierListed(page, 'local');
  await expectStorageTierNotListed(page, tier.name);

  // 4. Create a tier using the existing backend and the supplied parameters.
  await openCreateStorageTierPage(page);
  await createStorageTier(page, tier);

  // 5. Return to the tier list and verify the new tier appears.
  await openStorageTierListPage(page);
  await expectStorageTierListed(page, tier.name);
  await expectStorageTierConfiguration(page, tier);
});
