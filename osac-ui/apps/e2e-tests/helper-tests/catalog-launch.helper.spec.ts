import { expect, test } from '@playwright/test';

import { launchCatalogItem } from '../../../libs/test-utils/src/catalog.page';

test('launches the selected catalog item directly to the general step', async ({ page }) => {
  await page.route('**/catalog/vms/item-1', (route) =>
    route.fulfill({
      contentType: 'text/html',
      body: '<button onclick="location.href=\'/catalog/vms/item-1/launch\'">Launch instance</button>',
    }),
  );
  await page.route('**/catalog/vms/item-1/launch', (route) =>
    route.fulfill({
      contentType: 'text/html',
      body: `<nav aria-label="Breadcrumb"><a href="/catalog/vms/item-1">Test item</a></nav>
        <h1>Create a virtual machine</h1><label for="metadata-name">Name</label>
        <input id="metadata-name">`,
    }),
  );
  await page.goto('/catalog/vms/item-1');
  expect(await launchCatalogItem(page, 'Test item')).toBe('item-1');
  await expect(page.getByLabel('Name', { exact: true })).toBeVisible();
});

test('rejects a launch breadcrumb referring to another catalog item', async ({ page }) => {
  await page.route('**/catalog/vms/item-1', (route) =>
    route.fulfill({
      contentType: 'text/html',
      body: '<button onclick="location.href=\'/catalog/vms/item-1/launch\'">Launch instance</button>',
    }),
  );
  await page.route('**/catalog/vms/item-1/launch', (route) =>
    route.fulfill({
      contentType: 'text/html',
      body: `<nav aria-label="Breadcrumb"><a href="/catalog/vms/item-2">Test item</a></nav>
        <h1>Create a virtual machine</h1>`,
    }),
  );
  await page.goto('/catalog/vms/item-1');
  await expect(launchCatalogItem(page, 'Test item')).rejects.toThrow();
});
