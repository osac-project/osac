import { test } from '@playwright/test';

import { diskImageTestData } from './test_data';
import {
  createDiskImage,
  expectDiskImageListed,
  openCreateDiskImagePage,
} from '../../../../libs/test-utils/src/disk_images.page';
import { openAndLoginWithEnvironment } from '../../../../libs/test-utils/src/general/open_and_login';
import { openDiskImagesPage } from '../../../../libs/test-utils/src/main.page';

test('creates a VM-ready disk image from Infrastructure', async ({ page }) => {
  // Allow remote login, navigation, and disk image creation to complete.
  test.setTimeout(120_000);
  const diskImageName = process.env.OSAC_DISK_IMAGE_NAME;
  if (!diskImageName) {
    test.skip(true, 'Set OSAC_DISK_IMAGE_NAME to pair creation with lifecycle and deletion.');
    return;
  }

  const diskImage = { ...diskImageTestData, name: diskImageName };

  // 1. Open the browser and log in.
  await openAndLoginWithEnvironment(page, '/');

  // 2. Open Infrastructure > Disk images and verify the list page.
  await openDiskImagesPage(page);

  // 3. Open the creation form and verify its required fields.
  await openCreateDiskImagePage(page);

  // 4. Create the VM-ready image configured in extravars.yaml.
  await createDiskImage(page, diskImage);

  // 5. Return to the Disk images list and verify the new image is listed.
  await expectDiskImageListed(page, diskImage.name);
});
