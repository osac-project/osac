import { test } from '@playwright/test';

import { diskImageNameOrSkip, diskImagesTestData } from './test_data';
import {
  expectDiskImageLifecycle,
  openDiskImageDetails,
  transitionDiskImageLifecycle,
} from '../../../../libs/test-utils/src/disk_images.page';
import { openAndLoginWithEnvironment } from '../../../../libs/test-utils/src/general/open_and_login';
import { openDiskImagesPage } from '../../../../libs/test-utils/src/main.page';

test(`exercises lifecycle transitions for ${diskImagesTestData.name}`, async ({ page }) => {
  // Allow remote login and lifecycle updates to complete.
  test.setTimeout(180_000);
  const diskImageName = diskImageNameOrSkip();
  if (!diskImageName) {
    return;
  }

  // 1. Open the browser and log in.
  await openAndLoginWithEnvironment(page, '/');

  // 2. Open Infrastructure > Disk images and the requested image's details.
  await openDiskImagesPage(page);
  await openDiskImageDetails(page, diskImageName);

  // 3. Verify both lifecycle paths using the shared actions from extravars.yaml. Button-state
  // assertions are paused for OSAC-6385; each call clicks the action and checks the lifecycle.
  const [deprecate, obsolete, reactivate] = diskImagesTestData.lifecycleActions;
  await expectDiskImageLifecycle(page, 'Available');
  await transitionDiskImageLifecycle(page, obsolete, 'Obsolete');
  await transitionDiskImageLifecycle(page, reactivate, 'Available');
  await transitionDiskImageLifecycle(page, deprecate, 'Deprecated');
  await transitionDiskImageLifecycle(page, reactivate, 'Available');
});
