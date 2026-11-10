import { test } from '@playwright/test';

import { deleteDiskImageFlow } from './delete_flows';
import { diskImageNameOrSkip, diskImagesTestData } from './test_data';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';

test(`deletes disk image ${diskImagesTestData.name}`, async ({ page }) => {
  // Allow image creation when needed, deletion, login, and UI actions to complete.
  test.setTimeout(300_000);
  const diskImageName = diskImageNameOrSkip();
  if (!diskImageName) {
    logProgress('disk-image-delete', 'Skipped: disk_image.name is not configured.');
    return;
  }
  logProgress('disk-image-delete', `Starting flow for "${diskImageName}".`);

  await testStep('disk-image-delete', 'Log in as the admin test user', () =>
    openAndLogin(
      page,
      '/',
      process.env.OSAC_ADMIN_USERNAME ?? '',
      process.env.OSAC_ADMIN_PASSWORD ?? '',
      'admin',
    ),
  );

  await testStep('disk-image-delete', `Delete disk image "${diskImageName}"`, () =>
    deleteDiskImageFlow(page, { ...diskImagesTestData, name: diskImageName }),
  );
});
