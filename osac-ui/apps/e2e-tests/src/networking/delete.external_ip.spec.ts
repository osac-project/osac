import { test } from '@playwright/test';

import {
  loginAsProviderAdmin,
  loginAsTenantAdmin,
  resolveTenantAdminIdentity,
  switchIdentity,
} from './identity';
import {
  externalIpName,
  externalIpPoolName,
  externalIpPoolProvisioningSkipReason,
} from './test_data';
import {
  deleteExternalIpIfPresent,
  deleteExternalIpPoolIfPresent,
  openExternalIpListPage,
  openExternalIpPoolListPage,
} from '../../../../libs/test-utils/src/external_ips.page';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';

test('deletes the external IP created by the create flow', async ({ page }) => {
  // Teardown for the external-ip project: removes the External IP as its tenant
  // and, when this suite provisioned it, the pool as the provider admin.
  test.setTimeout(900_000);
  const flow = 'external-ip-delete';
  const identity = await resolveTenantAdminIdentity();
  logProgress(flow, `Starting flow for "${externalIpName}".`);

  await testStep(flow, 'Log in as the tenant admin', () => loginAsTenantAdmin(page, identity));

  await testStep(flow, 'Open Networking > External IPs', () => openExternalIpListPage(page));

  const deleted = await testStep(
    flow,
    `Delete external IP "${externalIpName}" when it is present`,
    () => deleteExternalIpIfPresent(page, externalIpName),
  );

  logProgress(
    flow,
    deleted
      ? `Deleted external IP "${externalIpName}".`
      : `External IP "${externalIpName}" was already absent, so nothing was deleted.`,
  );

  const poolSkipReason = externalIpPoolProvisioningSkipReason();
  if (poolSkipReason) {
    logProgress(flow, `Leaving the pool alone: ${poolSkipReason}`);
    return;
  }

  await testStep(flow, 'Log out of the tenant session', () => switchIdentity(page));

  await testStep(flow, 'Log in as the provider admin', () => loginAsProviderAdmin(page));

  await testStep(flow, 'Open Infrastructure > External IP pools', () =>
    openExternalIpPoolListPage(page),
  );

  await testStep(flow, `Delete pool "${externalIpPoolName}" when it is still present`, () =>
    deleteExternalIpPoolIfPresent(page, externalIpPoolName),
  );
});
