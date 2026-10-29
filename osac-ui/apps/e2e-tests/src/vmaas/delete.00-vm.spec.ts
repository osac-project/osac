import { test } from '@playwright/test';
import { access } from 'node:fs/promises';

import { tenantNameOrSkip, vmTestName } from './test_data';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
import {
  readTenantCredentials,
  tenantCredentialsFilePath,
} from '../../../../libs/test-utils/src/tenants.page';
import {
  deleteVm,
  openVmDetailsPage,
  openVmsPage,
  waitForVmToBeDeleted,
} from '../../../../libs/test-utils/src/vms.page';

test(`deletes VM ${vmTestName}`, async ({ page }) => {
  test.setTimeout(300_000);
  const tenantName = tenantNameOrSkip();
  if (!tenantName) {
    logProgress('vm-delete', 'Skipped: tenant_name is not configured.');
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

  logProgress('vm-delete', `Starting flow for "${vmTestName}".`);
  const tenantCredentials = await testStep('vm-delete', 'Read generated tenant credentials', () =>
    readTenantCredentials(credentialsFile),
  );

  await testStep('vm-delete', 'Log in as the tenant user', () =>
    openAndLogin(page, '/', tenantCredentials.username, tenantCredentials.password, 'tenant_type'),
  );

  await testStep('vm-delete', 'Open the tenant Virtual Machines page', () => openVmsPage(page));
  await testStep('vm-delete', `Open VM "${vmTestName}" details`, () =>
    openVmDetailsPage(page, vmTestName),
  );

  await testStep('vm-delete', `Delete VM "${vmTestName}"`, () => deleteVm(page, vmTestName));
  await testStep('vm-delete', `Verify VM "${vmTestName}" is deleted`, () =>
    waitForVmToBeDeleted(page, vmTestName),
  );
});
