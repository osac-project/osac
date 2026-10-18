import { test } from '@playwright/test';

import {
  deleteDiskImage,
  includeObsoleteDiskImages,
  openDiskImageDetails,
} from '../../../../libs/test-utils/src/disk_images.page';
import { openAndLoginWithEnvironment } from '../../../../libs/test-utils/src/general/open_and_login';
import { openDiskImagesPage } from '../../../../libs/test-utils/src/main.page';

test('deletes the requested obsolete disk image', async ({ page }) => {
  // Allow remote login and deletion to complete.
  test.setTimeout(120_000);

  const diskImageName = process.env.OSAC_DISK_IMAGE_NAME;
  if (!diskImageName) {
    test.skip(true, 'OSAC_DISK_IMAGE_NAME is not set.');
    return;
  }

  // 1. Open the browser and log in.
  await openAndLoginWithEnvironment(page, '/');

  // 2. Open Infrastructure > Disk images and the image made obsolete by the lifecycle flow.
  await openDiskImagesPage(page);
  await includeObsoleteDiskImages(page);
  await openDiskImageDetails(page, diskImageName);

  // 3. Confirm deletion and verify that the image is no longer in the table.
  await deleteDiskImage(page, diskImageName);
});
