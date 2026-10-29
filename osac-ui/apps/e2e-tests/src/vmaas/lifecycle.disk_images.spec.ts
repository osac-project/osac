import { test } from '@playwright/test';

import { diskImageNameOrSkip, diskImagesTestData } from './test_data';
import {
  expectDiskImageLifecycle,
  openDiskImageDetails,
  transitionDiskImageLifecycle,
} from '../../../../libs/test-utils/src/disk_images.page';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
import { openDiskImagesPage } from '../../../../libs/test-utils/src/main.page';

test(`exercises lifecycle transitions for ${diskImagesTestData.name}`, async ({ page }) => {
  // Allow remote login and lifecycle updates to complete.
  test.setTimeout(180_000);
  const diskImageName = diskImageNameOrSkip();
  if (!diskImageName) {
    logProgress('disk-image-lifecycle', 'Skipped: disk_image.name is not configured.');
    return;
  }
  logProgress('disk-image-lifecycle', `Starting flow for "${diskImageName}".`);

  await testStep('disk-image-lifecycle', 'Log in as the admin test user', () =>
    openAndLogin(
      page,
      '/',
      process.env.OSAC_ADMIN_USERNAME ?? '',
      process.env.OSAC_ADMIN_PASSWORD ?? '',
      'admin',
    ),
  );

  await testStep('disk-image-lifecycle', 'Open the configured disk image', async () => {
    await openDiskImagesPage(page);
    await openDiskImageDetails(page, diskImageName);
  });

  const [deprecate, obsolete, reactivate] = diskImagesTestData.lifecycleActions;
  await testStep(
    'disk-image-lifecycle',
    `Verify disk image "${diskImageName}" is Available before lifecycle transitions`,
    () => expectDiskImageLifecycle(page, 'Available'),
  );
  await testStep('disk-image-lifecycle', 'Transition disk image to Obsolete', () =>
    transitionDiskImageLifecycle(page, obsolete, 'Obsolete'),
  );
  await testStep('disk-image-lifecycle', 'Reactivate disk image to Available', () =>
    transitionDiskImageLifecycle(page, reactivate, 'Available'),
  );
  await testStep('disk-image-lifecycle', 'Deprecate disk image', () =>
    transitionDiskImageLifecycle(page, deprecate, 'Deprecated'),
  );
  await testStep('disk-image-lifecycle', 'Reactivate disk image to Available', () =>
    transitionDiskImageLifecycle(page, reactivate, 'Available'),
  );
});
