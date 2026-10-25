import { test } from '@playwright/test';

import { diskImagesTestData, tenantNameOrSkip } from './test_data';
import {
  deleteDiskImage,
  includeObsoleteDiskImages,
  openDiskImageDetails,
} from '../../../../libs/test-utils/src/disk_images.page';
import { openAndLoginWithEnvironment } from '../../../../libs/test-utils/src/general/open_and_login';
import { openDiskImagesPage } from '../../../../libs/test-utils/src/main.page';

test(`deletes disk image ${diskImagesTestData.name}`, async ({ page }) => {
  // Allow the 120-second deletion wait plus login and UI actions to complete.
  test.setTimeout(180_000);
  if (!tenantNameOrSkip()) {
    return;
  }

  // 1. Open the browser and log in.
  await openAndLoginWithEnvironment(page, '/');

  // 2. Open Infrastructure > Disk images and the configured image.
  await openDiskImagesPage(page);
  await includeObsoleteDiskImages(page);
  await openDiskImageDetails(page, diskImagesTestData.name);

  // 3. Confirm deletion and verify that the image is no longer in the table.
  await deleteDiskImage(page, diskImagesTestData.name);
});
