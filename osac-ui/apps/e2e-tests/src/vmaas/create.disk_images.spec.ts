import { test } from '@playwright/test';

import { diskImagesTestData, tenantNameOrSkip } from './test_data';
import {
  createDiskImage,
  expectDiskImageListed,
  expectDiskImageNotToExist,
  openCreateDiskImagePage,
} from '../../../../libs/test-utils/src/disk_images.page';
import { openAndLoginWithEnvironment } from '../../../../libs/test-utils/src/general/open_and_login';
import { openDiskImagesPage } from '../../../../libs/test-utils/src/main.page';

test(`creates disk image ${diskImagesTestData.name} from Infrastructure`, async ({ page }) => {
  // Allow remote login, navigation, and disk image creation to complete.
  test.setTimeout(120_000);
  if (!tenantNameOrSkip()) {
    return;
  }

  const diskImage = diskImagesTestData;

  // 1. Open the browser and log in.
  await openAndLoginWithEnvironment(page, '/');

  // 2. Open Infrastructure > Disk images and verify the list page.
  await openDiskImagesPage(page);

  // 3. Verify the configured name is available before creating the image.
  await expectDiskImageNotToExist(page, diskImage.name);

  // 4. Open the creation form and verify its required fields.
  await openCreateDiskImagePage(page);

  // 5. Create the VM-ready image configured in extravars.yaml.
  await createDiskImage(page, diskImage);

  // 6. Return to the Disk images list and verify the new image is listed.
  await expectDiskImageListed(page, diskImage.name);
});
