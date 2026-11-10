import { expect, test } from '@playwright/test';
import { access, writeFile } from 'node:fs/promises';

import { markResizeResourceOwned } from './resize/ownership';
import { catalogOwnershipFilePath, catalogTestData } from './test_data';
import { CatalogCreatePage } from '../../../../libs/locators/catalog';
import {
  expectCatalogItemListed,
  expectCatalogItemNotListed,
  openCreateCatalogItemPage,
} from '../../../../libs/test-utils/src/catalog.page';
import { selectOption } from '../../../../libs/test-utils/src/general/form_controls';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
import { openCatalogPage } from '../../../../libs/test-utils/src/main.page';

test('creates a catalog item through the Catalog UI and lists it in Catalog', async ({ page }) => {
  test.setTimeout(180_000);
  logProgress('catalog-create', `Starting flow for "${catalogTestData.name}".`);

  await testStep('catalog-create', 'Log in as the admin test user', () =>
    openAndLogin(
      page,
      '/',
      process.env.OSAC_ADMIN_USERNAME ?? '',
      process.env.OSAC_ADMIN_PASSWORD ?? '',
      'admin',
    ),
  );

  await testStep('catalog-create', 'Open the Catalog page', () => openCatalogPage(page));

  await testStep(
    'catalog-create',
    `Verify catalog item "${catalogTestData.name}" does not exist before creation`,
    () => expectCatalogItemNotListed(page, catalogTestData.name),
  );

  const ownershipFile = catalogOwnershipFilePath(catalogTestData.name);
  await testStep('catalog-create', 'Verify catalog ownership marker is not present', async () => {
    try {
      await access(ownershipFile);
      throw new Error(
        `Catalog item "${catalogTestData.name}" already has an ownership marker for this run.`,
      );
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'ENOENT') {
        throw error;
      }
    }
  });

  await testStep('catalog-create', 'Open the catalog item creation wizard', () =>
    openCreateCatalogItemPage(page),
  );

  await testStep('catalog-create', 'Complete the catalog item General step', async () => {
    await page.locator(...CatalogCreatePage.globalScope).click();
    await page.getByRole(...CatalogCreatePage.name).fill(catalogTestData.name);
    await page.getByRole(...CatalogCreatePage.description).fill(catalogTestData.description);
    const selectedTemplate = await selectOption(
      page,
      CatalogCreatePage.template,
      catalogTestData.template,
      0,
      true,
    );
    expect(selectedTemplate).toBe(catalogTestData.template);
    await page.getByRole(...CatalogCreatePage.next).click();
  });

  await testStep(
    'catalog-create',
    'Complete the catalog item Compute configuration step',
    async () => {
      await page.locator(...CatalogCreatePage.guestOsLinuxCard).click();
      await selectOption(page, CatalogCreatePage.diskImage, catalogTestData.fields.diskImage.name);
      await selectOption(
        page,
        CatalogCreatePage.instanceType,
        catalogTestData.fields.instanceType.name,
      );
      await page.getByRole(...CatalogCreatePage.next).click();
    },
  );

  await testStep('catalog-create', 'Complete the catalog item Storage step', async () => {
    await page
      .getByRole(...CatalogCreatePage.bootDiskSize)
      .fill(String(catalogTestData.fields.bootDisk.sizeGib));
    await page.getByRole(...CatalogCreatePage.next).click();
  });

  await testStep('catalog-create', 'Skip the catalog item Access & Initial run step', () =>
    page.getByRole(...CatalogCreatePage.next).click(),
  );

  await testStep('catalog-create', 'Create the catalog item', async () => {
    await expect(page.getByRole(...CatalogCreatePage.review)).toBeVisible();
    await page.getByRole(...CatalogCreatePage.create).click();
    await expect(page).toHaveURL(
      (url) => /^\/catalog\/vm\/[^/]+$/.test(url.pathname) && url.pathname !== '/catalog/vm/create',
    );
    await writeFile(ownershipFile, '', {
      encoding: 'utf8',
      flag: 'wx',
      mode: 0o600,
    });
    await expect(
      page.getByRole('heading', { name: catalogTestData.name, exact: true }),
    ).toBeVisible();
  });

  await testStep('catalog-create', 'Publish the catalog item', async () => {
    await page.getByRole(...CatalogCreatePage.publish).click();
    await expect(page.getByRole(...CatalogCreatePage.unpublished)).toBeVisible();
  });

  await testStep(
    'catalog-create',
    `Verify catalog item "${catalogTestData.name}" is visible in the UI`,
    async () => {
      await openCatalogPage(page);
      await expectCatalogItemListed(
        page,
        catalogTestData.name,
        catalogTestData.status,
        catalogTestData.usage,
        {
          instanceType: catalogTestData.fields.instanceType.name,
          diskImage: catalogTestData.fields.diskImage.name,
        },
      );
    },
  );
  if (test.info().project.name === 'vmaas-resize-catalog') {
    await testStep('catalog-create', 'Record resize prerequisite ownership', () =>
      markResizeResourceOwned('catalog'),
    );
  }
});
