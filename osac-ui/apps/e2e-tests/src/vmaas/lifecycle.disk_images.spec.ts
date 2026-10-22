import { test } from '@playwright/test';

import { diskImageTestData, tenantNameOrSkip } from './test_data';
import {
  expectDiskImageLifecycle,
  openDiskImageDetails,
  transitionDiskImageLifecycle,
} from '../../../../libs/test-utils/src/disk_images.page';
import { openAndLoginWithEnvironment } from '../../../../libs/test-utils/src/general/open_and_login';
import { openDiskImagesPage } from '../../../../libs/test-utils/src/main.page';

test('exercises disk image lifecycle transitions', async ({ page }) => {
  // Allow remote login and lifecycle updates to complete.
  test.setTimeout(180_000);
  if (!tenantNameOrSkip()) {
    return;
  }

  // 1. Open the browser and log in.
  await openAndLoginWithEnvironment(page, '/');

  // 2. Open Infrastructure > Disk images and the requested image's details.
  await openDiskImagesPage(page);
  await openDiskImageDetails(page, diskImageTestData.name);

  // 3. Verify both lifecycle paths using the shared actions from extravars.yaml. Button-state
  // assertions are paused for OSAC-6385; each call clicks the action and checks the lifecycle.
  const [deprecate, obsolete, reactivate] = diskImageTestData.lifecycleActions;
  await expectDiskImageLifecycle(page, 'Available');
  await transitionDiskImageLifecycle(page, obsolete, 'Obsolete');
  await transitionDiskImageLifecycle(page, reactivate, 'Available');
  await transitionDiskImageLifecycle(page, deprecate, 'Deprecated');
  await transitionDiskImageLifecycle(page, reactivate, 'Available');
});
