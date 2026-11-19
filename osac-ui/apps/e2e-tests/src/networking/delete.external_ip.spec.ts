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
  // and, when this suite provisioned it, the pool as the provider admin. The
  // pool step does not depend on tenant credentials being available.
  test.setTimeout(900_000);
  const flow = 'external-ip-delete';

  const tenantIdentity = await testStep(flow, 'Resolve the tenant admin identity', async () => {
    try {
      return await resolveTenantAdminIdentity();
    } catch (error) {
      const reason = error instanceof Error ? error.message : String(error);
      logProgress(flow, `Skipping the External IP cleanup step: ${reason}`);
      return undefined;
    }
  });

  if (tenantIdentity) {
    await testStep(flow, 'Log in as the tenant admin', () =>
      loginAsTenantAdmin(page, tenantIdentity),
    );

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
        : `External IP "${externalIpName}" was already absent.`,
    );
  }

  const poolSkipReason = externalIpPoolProvisioningSkipReason();
  if (poolSkipReason) {
    logProgress(flow, `Leaving the pool alone: ${poolSkipReason}`);
    return;
  }

  await testStep(flow, 'Reset the browser session', () => switchIdentity(page).catch(() => {}));

  await testStep(flow, 'Log in as the provider admin', () => loginAsProviderAdmin(page));

  await testStep(flow, 'Open Infrastructure > External IP pools', () =>
    openExternalIpPoolListPage(page),
  );

  await testStep(flow, `Delete pool "${externalIpPoolName}" when it is still present`, () =>
    deleteExternalIpPoolIfPresent(page, externalIpPoolName),
  );
});
