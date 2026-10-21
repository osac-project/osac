import { test } from '@playwright/test';

import { diskImageTestData } from './test_data';
import {
  expectDiskImageLifecycle,
  openDiskImageDetails,
  transitionDiskImageLifecycle,
} from '../../../../libs/test-utils/src/disk_images.page';
import { openAndLoginWithEnvironment } from '../../../../libs/test-utils/src/general/open_and_login';
import { openDiskImagesPage } from '../../../../libs/test-utils/src/main.page';

const lifecycleByAction = {
  Deprecate: 'Deprecated',
  Obsolete: 'Obsolete',
  Reactivate: 'Available',
} as const;

test('exercises disk image lifecycle transitions and leaves it obsolete for deletion', async ({
  page,
}) => {
  // Allow remote login and lifecycle updates to complete.
  test.setTimeout(180_000);

  const diskImageName = process.env.OSAC_DISK_IMAGE_NAME;
  if (!diskImageName) {
    test.skip(true, 'OSAC_DISK_IMAGE_NAME is not set.');
    return;
  }

  // 1. Open the browser and log in.
  await openAndLoginWithEnvironment(page, '/');

  // 2. Open Infrastructure > Disk images and the requested image's details.
  await openDiskImagesPage(page);
  await openDiskImageDetails(page, diskImageName);

  // 3. Verify lifecycle text after each configured transition. Button-state assertions are
  // paused for OSAC-6385; each call clicks the action and checks the resulting lifecycle.
  await expectDiskImageLifecycle(page, 'Available');
  for (const action of diskImageTestData.lifecycleActions) {
    await transitionDiskImageLifecycle(page, action, lifecycleByAction[action]);
  }
});
