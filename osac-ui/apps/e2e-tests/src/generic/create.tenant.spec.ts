import { test } from '@playwright/test';

import { createTenantSshKey } from '../../../../libs/osac-cli/src/secret';
import {
  createTenantAdminRoleBinding,
  getTenantBreakGlassCredentials,
} from '../../../../libs/osac-cli/src/tenant_admin';
import { logout, openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
import { openTenantsPage } from '../../../../libs/test-utils/src/main.page';
import {
  createTenant,
  expectDefaultTenants,
  expectTenantNotToExist,
  expectTenantToBeSynced,
  expectTenantToExist,
  getTenantFromBrowser,
  openCreateTenantPage,
  readTenantCredentials,
  saveTenantCredentials,
  submitTenantAndReadIdentity,
  tenantCredentialsFilePath,
  updateTenantCredentialsPassword,
} from '../../../../libs/test-utils/src/tenants.page';
import { writeTenantRunState } from '../shared/run_state';
import { catalogTestRunId, tenantNameOrSkip, tenantPassword } from '../shared/test_data';

test('creates a tenant from the Administration tenants page', async ({ page }) => {
  // Allow backend reconciliation and tenant-admin provisioning waits to complete.
  test.setTimeout(420_000);
  const configuredTenantName = tenantNameOrSkip();
  if (!configuredTenantName) {
    logProgress('tenant-create', 'Skipped: tenant_name is not configured.');
    return;
  }
  const tenantBootstrapConfig = process.env.OSAC_E2E_TENANT_BOOTSTRAP_CONFIG;
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
    if (tenantBootstrapConfig) {
      // https://redhat.atlassian.net/browse/OSAC-7988 blocks the shared tenant readiness check.
      logProgress('tenant-create', 'Skipping the shared tenant readiness check (OSAC-7988).');
      return expectTenantToBeSynced(page, 'system');
    }
    await expectDefaultTenants(page);
  });

  await testStep(
    'tenant-create',
    `Verify tenant "${configuredTenantName}" does not exist before creation`,
    async () => {
      await expectTenantNotToExist(page, configuredTenantName);
    },
  );

  const createdTenant = await testStep(
    'tenant-create',
    `Create tenant "${configuredTenantName}"`,
    async () => {
      await openCreateTenantPage(page);
      if (!tenantBootstrapConfig) {
        await createTenant(page, configuredTenantName);
        return;
      }

      // https://redhat.atlassian.net/browse/OSAC-7989 blocks validation of the credentials dialog.
      const tenant = await submitTenantAndReadIdentity(page, configuredTenantName);
      await writeTenantRunState(catalogTestRunId, tenant);
      await page.goto('/admin/tenants');
      return tenant;
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

  const credentialsFile = tenantCredentialsFilePath(configuredTenantName);
  const tenantCredentials = await testStep(
    'tenant-create',
    'Read generated tenant break-glass credentials',
    async () => {
      if (!tenantBootstrapConfig) {
        return readTenantCredentials(credentialsFile);
      }
      const tenant = await getTenantFromBrowser(page, configuredTenantName);
      if (!createdTenant || tenant?.creationTimestamp !== createdTenant.creationTimestamp) {
        throw new Error('The tenant in the browser does not match the tenant created by this run.');
      }
      const secretId = tenant.breakGlassSecretId;
      if (!secretId) {
        throw new Error('The new test tenant has no break-glass secret reference.');
      }
      const credentials = await getTenantBreakGlassCredentials(
        configuredTenantName,
        secretId,
        tenantBootstrapConfig,
      );
      await saveTenantCredentials(configuredTenantName, credentials);
      return credentials;
    },
  );

  const replacementPassword = tenantPassword?.trim() || configuredTenantName;
  await testStep('tenant-create', 'Log in with the generated tenant credentials', async () => {
    await logout(page);
    logProgress(
      'tenant-create',
      tenantPassword?.trim()
        ? 'Using the configured replacement password for the tenant login.'
        : `Using tenant "${configuredTenantName}" as the replacement password.`,
    );
    const passwordUpdated = await openAndLogin(
      page,
      '/',
      tenantCredentials.username,
      tenantCredentials.password,
      'tenant_type',
      replacementPassword,
    );
    if (passwordUpdated) {
      await updateTenantCredentialsPassword(credentialsFile, replacementPassword);
    }
  });

  const tenantAdminCredentials = await testStep(
    'tenant-create',
    'Read updated tenant credentials',
    () => readTenantCredentials(credentialsFile),
  );

  await testStep(
    'tenant-create',
    'Log in as the admin test user to grant tenant-admin permissions',
    async () => {
      await logout(page);
      await openAndLogin(
        page,
        '/',
        process.env.OSAC_ADMIN_USERNAME ?? '',
        process.env.OSAC_ADMIN_PASSWORD ?? '',
        'admin',
      );
    },
  );

  await testStep(
    'tenant-create',
    `Grant tenant-admin permissions to "${tenantAdminCredentials.username}"`,
    () => createTenantAdminRoleBinding(configuredTenantName, tenantAdminCredentials.username),
  );

  await testStep(
    'tenant-create',
    'Log in with the generated tenant-admin credentials',
    async () => {
      await logout(page);
      await openAndLogin(
        page,
        '/',
        tenantAdminCredentials.username,
        tenantAdminCredentials.password,
        'tenant_admin',
      );
    },
  );

  await testStep(
    'tenant-create',
    `Create SSH public-key Secret for tenant "${configuredTenantName}"`,
    () => createTenantSshKey(configuredTenantName),
  );
});
