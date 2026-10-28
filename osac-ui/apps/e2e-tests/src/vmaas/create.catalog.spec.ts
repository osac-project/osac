import { expect, test } from '@playwright/test';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { stringify } from 'yaml';

import { catalogTestData } from './test_data';
import { getCatalogItems } from '../../../../libs/osac-cli/src/catalog';
import { expectOsacCommandSucceeded, runOsacCommand } from '../../../../libs/osac-cli/src/cli';
import { expectCatalogItemListed } from '../../../../libs/test-utils/src/catalog.page';
import { openAndLoginWithEnvironment } from '../../../../libs/test-utils/src/general/open_and_login';
import { openCatalogPage } from '../../../../libs/test-utils/src/main.page';

test('creates a catalog item with the osac CLI and lists it in Catalog', async ({ page }) => {
  test.setTimeout(180_000);

  // Verify the name is available before attempting creation.
  const existingItems = await getCatalogItems(catalogTestData.name);
  if (existingItems.length !== 0) {
    throw new Error(
      `Catalog item '${catalogTestData.name}' already exists. ` +
        'Choose another catalog.name in extravars.yaml.',
    );
  }

  // The resource fields are configured in the manifest because the metadata-only
  // CLI flags cannot set typed catalog field policies.
  const manifest = {
    '@type': 'type.googleapis.com/osac.public.v1.ComputeInstanceCatalogItem',
    metadata: { name: catalogTestData.name },
    title: catalogTestData.title,
    description: catalogTestData.description,
    template: {
      id: catalogTestData.template,
      shared: true,
    },
    published: true,
    fields: {
      instance_type: {
        editable: {
          default_value: {
            name: catalogTestData.fields.instanceType.name,
            shared: catalogTestData.fields.instanceType.shared,
          },
        },
      },
      boot_disk: {
        size_gib: {
          editable: {
            default_value: catalogTestData.fields.bootDisk.sizeGib,
          },
        },
      },
      disk_image: {
        locked: {
          name: catalogTestData.fields.diskImage.name,
          shared: catalogTestData.fields.diskImage.shared,
        },
      },
    },
  };

  const manifestDirectory = await mkdtemp(path.join(os.tmpdir(), 'osac-catalog-'));
  const manifestPath = path.join(manifestDirectory, 'catalog-item.yaml');
  await writeFile(manifestPath, stringify(manifest), { mode: 0o600 });

  try {
    const createResult = await runOsacCommand(['create', '-f', manifestPath]);
    expectOsacCommandSucceeded(createResult, `Create catalog item '${catalogTestData.name}'`);
  } finally {
    await rm(manifestDirectory, { force: true, recursive: true });
  }

  // Verify the created object and its user-visible metadata through the CLI.
  const createdItems = await getCatalogItems(catalogTestData.name);
  expect(createdItems).toHaveLength(1);
  expect(createdItems[0]?.metadata?.name).toBe(catalogTestData.name);
  expect(createdItems[0]?.title).toBe(catalogTestData.title);
  expect(createdItems[0]?.description).toBe(catalogTestData.description);
  expect(createdItems[0]?.template?.id).toBeTruthy();
  expect(createdItems[0]?.fields?.instance_type?.editable?.default_value?.name).toBe(
    catalogTestData.fields.instanceType.name,
  );
  expect(createdItems[0]?.fields?.boot_disk?.size_gib?.editable?.default_value).toBe(
    catalogTestData.fields.bootDisk.sizeGib,
  );
  expect(createdItems[0]?.fields?.disk_image?.locked?.name).toBe(
    catalogTestData.fields.diskImage.name,
  );

  // Open the browser, authenticate, and verify the created catalog item in the UI.
  await openAndLoginWithEnvironment(page, '/');
  await openCatalogPage(page);
  await expectCatalogItemListed(
    page,
    catalogTestData.name,
    catalogTestData.type,
    catalogTestData.status,
    catalogTestData.usage,
    {
      vcpus: catalogTestData.fields.instanceType.vcpus,
      memoryGib: catalogTestData.fields.instanceType.memoryGib,
      storageGib: catalogTestData.fields.bootDisk.sizeGib,
      diskImage: catalogTestData.fields.diskImage.name,
    },
  );
});
