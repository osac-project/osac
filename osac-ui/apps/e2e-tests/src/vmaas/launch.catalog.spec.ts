import { test } from '@playwright/test';
import { access } from 'node:fs/promises';

import { catalogTestData, tenantNameOrSkip } from './test_data';
import {
  launchCatalogItem,
  openCatalogItemDetails,
} from '../../../../libs/test-utils/src/catalog.page';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
import { openCatalogPage } from '../../../../libs/test-utils/src/main.page';
import {
  readTenantCredentials,
  tenantCredentialsFilePath,
} from '../../../../libs/test-utils/src/tenants.page';

test('tenant launches the published VM catalog item', async ({ page }) => {
  test.setTimeout(120_000);
  const tenantName = tenantNameOrSkip();
  if (!tenantName) {
    logProgress('catalog-launch', 'Skipped: tenant_name is not configured.');
    return;
  }

  const credentialsFile = tenantCredentialsFilePath(tenantName);
  try {
    await access(credentialsFile);
  } catch {
    test.skip(
      true,
      `Tenant credentials were not found at ${credentialsFile}; run the full flow first.`,
    );
    return;
  }

  logProgress('catalog-launch', `Starting flow for "${catalogTestData.name}".`);
  const tenantCredentials = await testStep(
    'catalog-launch',
    'Read generated tenant credentials',
    () => readTenantCredentials(credentialsFile),
  );

  await testStep('catalog-launch', 'Log in as the tenant user', () =>
    openAndLogin(page, '/', tenantCredentials.username, tenantCredentials.password, 'tenant_type'),
  );

  await testStep('catalog-launch', 'Open the tenant Catalog', () => openCatalogPage(page));
  await testStep(
    'catalog-launch',
    `Select the published VM catalog item "${catalogTestData.name}"`,
    () => openCatalogItemDetails(page, catalogTestData.name),
  );
  await testStep('catalog-launch', 'Click Launch instance', () => launchCatalogItem(page));
});
