import { test } from '@playwright/test';

import { tenantNameOrSkip } from './test_data';
import { openAndLoginWithEnvironment } from '../../../../libs/test-utils/src/general/open_and_login';
import { openTenantsPage } from '../../../../libs/test-utils/src/main.page';
import {
  createTenant,
  expectDefaultTenants,
  expectTenantNotToExist,
  openCreateTenantPage,
} from '../../../../libs/test-utils/src/tenants.page';

test('creates a tenant from the Administration tenants page', async ({ page }) => {
  // Allow backend reconciliation and tenant creation waits to complete.
  test.setTimeout(120_000);
  const configuredTenantName = tenantNameOrSkip();
  if (!configuredTenantName) {
    return;
  }

  // 1. Open the browser and log in.
  await openAndLoginWithEnvironment(page, '/');

  // 2. Open Administration > Tenants and verify the Tenants page.
  await openTenantsPage(page);

  // 3. Verify that the default tenants exist and are synced.
  await expectDefaultTenants(page);

  // 4. Verify that the requested tenant does not already exist.
  await expectTenantNotToExist(page, configuredTenantName);

  // 5. Open the tenant creation page and create the requested tenant.
  await openCreateTenantPage(page);
  const credentialsFile = await createTenant(page, configuredTenantName);
  process.env.OSAC_TENANT_CREDENTIALS_FILE = credentialsFile;
});
