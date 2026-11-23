import { expect, test } from '@playwright/test';
import { access } from 'node:fs/promises';

import { deleteTenantSshKey } from '../../../../libs/osac-cli/src/secret';
import { deleteTenantAdmin } from '../../../../libs/osac-cli/src/tenant_admin';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
import { openTenantsPage } from '../../../../libs/test-utils/src/main.page';
import {
  deleteTenant,
  expectTenantNotToExist,
  expectTenantToExist,
  getTenantFromBrowser,
  readTenantCredentials,
  removeTenantCredentials,
  tenantCredentialsFilePath,
} from '../../../../libs/test-utils/src/tenants.page';
import {
  readTenantRunState,
  removeTenantRunState,
  removeVmaasFullFlowRunState,
} from '../shared/run_state';
import { catalogTestRunId, tenantNameOrSkip } from '../shared/test_data';

test('deletes the requested tenant', async ({ page }) => {
  // Allow the 300-second backend deletion wait plus login and UI actions to complete.
  test.setTimeout(420_000);
  const configuredTenantName = tenantNameOrSkip();
  if (!configuredTenantName) {
    logProgress('tenant-delete', 'Skipped: tenant_name is not configured.');
    return;
  }
  const createdTenant = await readTenantRunState(catalogTestRunId);
  const fullMode = (process.env.OSAC_VM_FLOW_MODE ?? 'full') === 'full';
  if (!createdTenant && fullMode && process.env.OSAC_E2E_TENANT_BOOTSTRAP_CONFIG) {
    logProgress('tenant-delete', 'No tenant creation was recorded for this run.');
    return;
  }
  logProgress('tenant-delete', `Starting flow for "${configuredTenantName}".`);

  await testStep('tenant-delete', 'Log in as the admin test user', async () => {
    await openAndLogin(
      page,
      '/',
      process.env.OSAC_ADMIN_USERNAME ?? '',
      process.env.OSAC_ADMIN_PASSWORD ?? '',
      'admin',
    );
  });

  await testStep('tenant-delete', 'Open Administration > Tenants', async () => {
    await openTenantsPage(page);
  });

  const credentialsFile = tenantCredentialsFilePath(configuredTenantName);
  if (createdTenant) {
    const tenant = await getTenantFromBrowser(page, configuredTenantName);
    if (
      createdTenant.id !== configuredTenantName ||
      (tenant && tenant.creationTimestamp !== createdTenant.creationTimestamp)
    ) {
      throw new Error('The current tenant does not match the tenant created by this run.');
    }
    if (!tenant) {
      await removeTenantCredentials(configuredTenantName);
      await removeTenantRunState(catalogTestRunId);
      await removeVmaasFullFlowRunState();
      return;
    }
  }

  await testStep(
    'tenant-delete',
    `Verify tenant "${configuredTenantName}" exists before deletion`,
    () => expectTenantToExist(page, configuredTenantName),
  );
  const credentialsSaved =
    !createdTenant ||
    (await access(credentialsFile).then(
      () => true,
      (error: NodeJS.ErrnoException) => {
        if (error.code === 'ENOENT') {
          return false;
        }
        throw error;
      },
    ));
  if (credentialsSaved) {
    const username = createdTenant
      ? `${configuredTenantName}-osac-break-glass`
      : (await readTenantCredentials(credentialsFile)).username;
    await testStep(
      'tenant-delete',
      `Remove tenant-admin user and role binding for "${configuredTenantName}"`,
      () => deleteTenantAdmin(configuredTenantName, username),
    );
    await testStep(
      'tenant-delete',
      `Delete SSH public-key Secret for tenant "${configuredTenantName}"`,
      () => deleteTenantSshKey(configuredTenantName),
    );
  } else {
    logProgress(
      'tenant-delete',
      'Tenant credential setup stopped before creating a role binding or SSH key.',
    );
  }
  await testStep('tenant-delete', `Delete tenant "${configuredTenantName}"`, () =>
    deleteTenant(page, configuredTenantName),
  );
  await testStep(
    'tenant-delete',
    `Verify tenant "${configuredTenantName}" does not exist after deletion`,
    () => expectTenantNotToExist(page, configuredTenantName),
  );
  await testStep('tenant-delete', `Verify tenant credential file is removed after deletion`, () =>
    expect(access(credentialsFile)).rejects.toThrow(),
  );
  await testStep('tenant-delete', 'Remove VMaaS full-flow run state', async () => {
    await removeTenantRunState(catalogTestRunId);
    await removeVmaasFullFlowRunState();
  });
});
