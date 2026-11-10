import { test } from '@playwright/test';

import { diskImageNameOrSkip, diskImagesTestData } from './test_data';
import {
  deleteDiskImage,
  ensureDiskImageExists,
  expectDiskImageNotToExist,
  includeObsoleteDiskImages,
  openDiskImageDetails,
} from '../../../../libs/test-utils/src/disk_images.page';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
import { openDiskImagesPage } from '../../../../libs/test-utils/src/main.page';

test(`deletes disk image ${diskImagesTestData.name}`, async ({ page }) => {
  // Allow image creation when needed, deletion, login, and UI actions to complete.
  test.setTimeout(300_000);
  const diskImageName = diskImageNameOrSkip();
  if (!diskImageName) {
    logProgress('disk-image-delete', 'Skipped: disk_image.name is not configured.');
    return;
  }
  logProgress('disk-image-delete', `Starting flow for "${diskImageName}".`);

  await testStep('disk-image-delete', 'Log in as the admin test user', () =>
    openAndLogin(
      page,
      '/',
      process.env.OSAC_ADMIN_USERNAME ?? '',
      process.env.OSAC_ADMIN_PASSWORD ?? '',
      'admin',
    ),
  );

  await testStep('disk-image-delete', 'Open Infrastructure > Disk images', async () => {
    await openDiskImagesPage(page);
  });

  await testStep(
    'disk-image-delete',
    `Verify disk image "${diskImageName}" exists before deletion`,
    async () => {
      await ensureDiskImageExists(page, { ...diskImagesTestData, name: diskImageName });
    },
  );

  await testStep('disk-image-delete', 'Open the configured disk image', async () => {
    await includeObsoleteDiskImages(page);
    await openDiskImageDetails(page, diskImageName);
  });

  await testStep('disk-image-delete', `Delete disk image "${diskImageName}"`, () =>
    deleteDiskImage(page, diskImageName),
  );

  await testStep(
    'disk-image-delete',
    `Verify disk image "${diskImageName}" does not exist after deletion`,
    () => expectDiskImageNotToExist(page, diskImageName),
  );
});
