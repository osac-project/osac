import { test } from '@playwright/test';

import {
  loginAsProviderAdmin,
  loginAsTenantAdmin,
  resolveTenantAdminIdentity,
  switchIdentity,
} from './identity';
import {
  createdExternalIpId,
  createdExternalIpPoolId,
  forgetCreatedExternalIp,
  forgetCreatedExternalIpPool,
} from './run_state';
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
  // Teardown: deletes only the ids this run recorded. Without a record it
  // refuses to act, so a name collision can never remove someone else's IP.
  test.setTimeout(900_000);
  const flow = 'external-ip-delete';
  let tenantFailure: Error | undefined;

  try {
    const identity = await resolveTenantAdminIdentity();

    await testStep(flow, 'Log in as the tenant admin', () => loginAsTenantAdmin(page, identity));

    await testStep(flow, 'Open Networking > External IPs', () => openExternalIpListPage(page));

    const outcome = await testStep(
      flow,
      `Delete external IP "${externalIpName}" when it is present`,
      async (): Promise<'deleted' | 'absent' | 'skipped'> => {
        const expectedId = createdExternalIpId();
        if (!expectedId) {
          logProgress(flow, `No recorded id for "${externalIpName}"; refusing to delete by name.`);
          return 'skipped';
        }
        const result = await deleteExternalIpIfPresent(page, externalIpName, expectedId);
        if (result) {
          forgetCreatedExternalIp();
        }
        return result ? 'deleted' : 'absent';
      },
    );

    logProgress(
      flow,
      outcome === 'deleted'
        ? `Deleted external IP "${externalIpName}".`
        : outcome === 'absent'
          ? `External IP "${externalIpName}" was already absent.`
          : `External IP "${externalIpName}" was left in place: no ownership record.`,
    );
  } catch (error) {
    tenantFailure = error instanceof Error ? error : new Error(String(error));
    logProgress(flow, `Tenant cleanup did not complete: ${tenantFailure.message}`);
  }

  const poolSkipReason = externalIpPoolProvisioningSkipReason();
  if (poolSkipReason) {
    logProgress(flow, `Leaving the pool alone: ${poolSkipReason}`);
    if (tenantFailure) {
      throw tenantFailure;
    }
    return;
  }

  await testStep(flow, 'Reset the browser session', async () => {
    try {
      await switchIdentity(page);
    } catch {
      // A failed /api/logout must not leave the tenant session in place, or the
      // provider login below would verify the wrong identity.
      await page.context().clearCookies();
    }
  });

  await testStep(flow, 'Log in as the provider admin', () => loginAsProviderAdmin(page));

  await testStep(flow, 'Open Infrastructure > External IP pools', () =>
    openExternalIpPoolListPage(page),
  );

  const poolOutcome = await testStep(
    flow,
    `Delete pool "${externalIpPoolName}" when it is still present`,
    async (): Promise<'deleted' | 'absent' | 'skipped'> => {
      const expectedId = createdExternalIpPoolId();
      if (!expectedId) {
        logProgress(
          flow,
          `No recorded id for "${externalIpPoolName}"; refusing to delete by name.`,
        );
        return 'skipped';
      }
      const result = await deleteExternalIpPoolIfPresent(page, externalIpPoolName, expectedId);
      if (result) {
        forgetCreatedExternalIpPool();
      }
      return result ? 'deleted' : 'absent';
    },
  );

  logProgress(
    flow,
    poolOutcome === 'deleted'
      ? `Deleted pool "${externalIpPoolName}".`
      : poolOutcome === 'absent'
        ? `Pool "${externalIpPoolName}" was absent or retained.`
        : `Pool "${externalIpPoolName}" was left in place: no ownership record.`,
  );

  if (tenantFailure) {
    throw tenantFailure;
  }
});
