import { test } from '@playwright/test';

import { deleteTenantFlow } from './delete_flows';
import { removeVmaasFullFlowRunState } from './run_state';
import { tenantNameOrSkip } from './test_data';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';

test('deletes the requested tenant', async ({ page }) => {
  // Allow the 300-second backend deletion wait plus login and UI actions to complete.
  test.setTimeout(420_000);
  const configuredTenantName = tenantNameOrSkip();
  if (!configuredTenantName) {
    logProgress('tenant-delete', 'Skipped: tenant_name is not configured.');
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

  await testStep('tenant-delete', `Delete tenant "${configuredTenantName}"`, () =>
    deleteTenantFlow(page, configuredTenantName),
  );
  await testStep('tenant-delete', 'Remove VMaaS full-flow run state', async () => {
    await removeVmaasFullFlowRunState();
  });
});
