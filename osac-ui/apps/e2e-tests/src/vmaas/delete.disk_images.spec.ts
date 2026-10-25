import { test } from '@playwright/test';

import { diskImageNameOrSkip, diskImagesTestData } from './test_data';
import {
  deleteDiskImage,
  ensureDiskImageExists,
  includeObsoleteDiskImages,
  openDiskImageDetails,
} from '../../../../libs/test-utils/src/disk_images.page';
import { openAndLoginWithEnvironment } from '../../../../libs/test-utils/src/general/open_and_login';
import { openDiskImagesPage } from '../../../../libs/test-utils/src/main.page';

test(`deletes disk image ${diskImagesTestData.name}`, async ({ page }) => {
  // Allow image creation when needed, deletion, login, and UI actions to complete.
  test.setTimeout(300_000);
  const diskImageName = diskImageNameOrSkip();
  if (!diskImageName) {
    return;
  }

  // 1. Open the browser and log in.
  await openAndLoginWithEnvironment(page, '/');

  // 2. Open Infrastructure > Disk images and ensure the configured image exists.
  await openDiskImagesPage(page);
  await ensureDiskImageExists(page, { ...diskImagesTestData, name: diskImageName });

  // 3. Open the configured image, including if it is already obsolete.
  await includeObsoleteDiskImages(page);
  await openDiskImageDetails(page, diskImageName);

  // 4. Confirm deletion and verify that the image is no longer in the table.
  await deleteDiskImage(page, diskImageName);
});
