import { test } from '@playwright/test';

import { tenantNameOrSkip } from './test_data';
import { logout, openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
import { openTenantsPage } from '../../../../libs/test-utils/src/main.page';
import {
  createTenant,
  expectDefaultTenants,
  expectTenantNotToExist,
  expectTenantToBeSynced,
  expectTenantToExist,
  openCreateTenantPage,
  readTenantCredentials,
} from '../../../../libs/test-utils/src/tenants.page';

test('creates a tenant from the Administration tenants page', async ({ page }) => {
  // Allow backend reconciliation and tenant creation waits to complete.
  test.setTimeout(120_000);
  const configuredTenantName = tenantNameOrSkip();
  if (!configuredTenantName) {
    logProgress('tenant-create', 'Skipped: tenant_name is not configured.');
    return;
  }
  logProgress('tenant-create', `Starting flow for "${configuredTenantName}".`);

  await testStep('tenant-create', 'Log in as the admin test user', async () => {
    await openAndLogin(
      page,
      '/',
      process.env.OSAC_ADMIN_USERNAME ?? '',
      process.env.OSAC_ADMIN_PASSWORD ?? '',
      'admin',
    );
  });

  await testStep('tenant-create', 'Open Administration > Tenants', async () => {
    await openTenantsPage(page);
  });

  await testStep('tenant-create', 'Verify default tenants are synced', async () => {
    await expectDefaultTenants(page);
  });

  await testStep(
    'tenant-create',
    `Verify tenant "${configuredTenantName}" does not exist before creation`,
    async () => {
      await expectTenantNotToExist(page, configuredTenantName);
    },
  );

  const credentialsFile = await testStep(
    'tenant-create',
    `Create tenant "${configuredTenantName}"`,
    async () => {
      await openCreateTenantPage(page);
      return createTenant(page, configuredTenantName);
    },
  );
  await testStep(
    'tenant-create',
    `Verify tenant "${configuredTenantName}" exists after creation`,
    () => expectTenantToExist(page, configuredTenantName),
  );
  await testStep(
    'tenant-create',
    `Verify tenant "${configuredTenantName}" is Synced after creation`,
    () => expectTenantToBeSynced(page, configuredTenantName),
  );
  const tenantCredentials = await testStep(
    'tenant-create',
    'Read generated tenant credentials',
    () => readTenantCredentials(credentialsFile),
  );

  await testStep('tenant-create', 'Log in with the generated tenant credentials', async () => {
    await logout(page);
    await openAndLogin(
      page,
      '/',
      tenantCredentials.username,
      tenantCredentials.password,
      'tenant_type',
    );
  });
});
