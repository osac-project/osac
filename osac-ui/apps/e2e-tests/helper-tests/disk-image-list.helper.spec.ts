import { expect, test } from '@playwright/test';

import { expectDiskImageNotToExist } from '../../../libs/test-utils/src/disk_images.page';

const listPath = '/admin/infrastructure/disk-images';
const imageName = 'disk-image-e2e';

test('accepts an empty result after including obsolete disk images', async ({ page }) => {
  await page.route(`**${listPath}*`, (route) => {
    const filters = new URL(route.request().url()).searchParams;
    const isFiltered =
      filters.get('search') === imageName && filters.get('showObsolete') === 'true';
    return route.fulfill({
      contentType: 'text/html',
      body: `<button aria-pressed="true">List view</button>
        <input id="disk-image-show-obsolete" type="checkbox" ${isFiltered ? 'checked' : ''}>
        <h2>${isFiltered ? 'No disk images match your filters' : 'No disk images found'}</h2>`,
    });
  });

  await page.goto(listPath);
  await expectDiskImageNotToExist(page, imageName);
});

test('detects an existing image when the disk image page opens in card view', async ({ page }) => {
  await page.route(`**${listPath}*`, (route) =>
    route.fulfill({
      contentType: 'text/html',
      body: `<button aria-pressed="false" onclick="this.setAttribute('aria-pressed', 'true');
          document.querySelector('[role=grid]').hidden = false">List view</button>
        <input id="disk-image-show-obsolete" type="checkbox" checked>
        <div role="grid" aria-label="Disk images" hidden>
          <a href="${listPath}/existing">${imageName}</a>
        </div>`,
    }),
  );

  await page.goto(listPath);
  await expect(expectDiskImageNotToExist(page, imageName)).rejects.toThrow(
    `Cannot create disk image "${imageName}": a disk image with this name already exists.`,
  );
});
