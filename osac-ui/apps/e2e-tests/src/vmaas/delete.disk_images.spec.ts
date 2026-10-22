import { test } from '@playwright/test';

import { diskImageTestData, tenantNameOrSkip } from './test_data';
import {
  deleteDiskImage,
  includeObsoleteDiskImages,
  openDiskImageDetails,
} from '../../../../libs/test-utils/src/disk_images.page';
import { openAndLoginWithEnvironment } from '../../../../libs/test-utils/src/general/open_and_login';
import { openDiskImagesPage } from '../../../../libs/test-utils/src/main.page';

test('deletes the configured disk image', async ({ page }) => {
  // Allow remote login and deletion to complete.
  test.setTimeout(120_000);
  if (!tenantNameOrSkip()) {
    return;
  }

  // 1. Open the browser and log in.
  await openAndLoginWithEnvironment(page, '/');

  // 2. Open Infrastructure > Disk images and the configured image.
  await openDiskImagesPage(page);
  await includeObsoleteDiskImages(page);
  await openDiskImageDetails(page, diskImageTestData.name);

  // 3. Confirm deletion and verify that the image is no longer in the table.
  await deleteDiskImage(page, diskImageTestData.name);
});
