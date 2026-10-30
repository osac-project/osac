import { test } from '@playwright/test';

import { diskImageNameOrSkip, diskImagesTestData } from './test_data';
import {
  createDiskImage,
  expectDiskImageListed,
  expectDiskImageNotToExist,
  openCreateDiskImagePage,
} from '../../../../libs/test-utils/src/disk_images.page';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
import { openDiskImagesPage } from '../../../../libs/test-utils/src/main.page';

test(`creates disk image ${diskImagesTestData.name} from Infrastructure`, async ({ page }) => {
  // Allow remote login, navigation, and disk image creation to complete.
  test.setTimeout(120_000);
  const diskImageName = diskImageNameOrSkip();
  if (!diskImageName) {
    logProgress('disk-image-create', 'Skipped: disk_image.name is not configured.');
    return;
  }
  logProgress('disk-image-create', `Starting flow for "${diskImageName}".`);

  const diskImage = { ...diskImagesTestData, name: diskImageName };

  await testStep('disk-image-create', 'Log in as the admin test user', () =>
    openAndLogin(
      page,
      '/',
      process.env.OSAC_ADMIN_USERNAME ?? '',
      process.env.OSAC_ADMIN_PASSWORD ?? '',
      'admin',
    ),
  );

  await testStep('disk-image-create', 'Open Infrastructure > Disk images', () =>
    openDiskImagesPage(page),
  );

  await testStep(
    'disk-image-create',
    `Verify disk image "${diskImage.name}" does not exist before creation`,
    () => expectDiskImageNotToExist(page, diskImage.name),
  );

  await testStep('disk-image-create', 'Open the disk image creation form', () =>
    openCreateDiskImagePage(page),
  );

  await testStep('disk-image-create', 'Create the configured disk image', () =>
    createDiskImage(page, diskImage),
  );

  await testStep(
    'disk-image-create',
    `Verify disk image "${diskImage.name}" exists after creation`,
    () => expectDiskImageListed(page, diskImage.name),
  );
});
