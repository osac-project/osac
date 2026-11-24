import { expect, test } from '@playwright/test';

import { loginAsProviderAdmin } from './identity';
import { forgetCreatedExternalIpPool, recordCreatedExternalIpPool } from './run_state';
import {
  externalIpPoolCidr,
  externalIpPoolName,
  externalIpPoolProvisioningSkipReason,
  externalIpPoolReadyStatus,
} from './test_data';
import {
  createExternalIpPool,
  expectExternalIpPoolNotListed,
  expectExternalIpPoolStatus,
  failExternalIpPoolListRequests,
  openExternalIpPoolListPage,
  restoreExternalIpPoolListRequests,
} from '../../../../libs/test-utils/src/external_ips.page';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';

test('creates the external IP pool the networking suite allocates from', async ({ page }) => {
  // Creating a pool and waiting for the provider to reconcile it is slow.
  test.setTimeout(900_000);
  const flow = 'external-ip-pool-create';
  const skipReason = externalIpPoolProvisioningSkipReason();
  if (skipReason) {
    logProgress(flow, `Skipped: ${skipReason}`);
    return;
  }
  logProgress(flow, `Starting flow for "${externalIpPoolName}".`);

  const createRequests: string[] = [];
  page.on('request', (request) => {
    if (/\/api\/fulfillment\/[\w.]*\.ExternalIPPools\/Create$/.test(request.url())) {
      createRequests.push(request.url());
    }
  });

  await testStep(flow, 'Log in as the provider admin', () => loginAsProviderAdmin(page));

  // Keep a recorded id for a pool an interrupted run created, so this run's
  // failed precondition does not strip the ownership cleanup needs.
  forgetCreatedExternalIpPool();

  await testStep(flow, 'Open Infrastructure > External IP pools', () =>
    openExternalIpPoolListPage(page),
  );

  await testStep(flow, `Verify pool "${externalIpPoolName}" does not exist before creation`, () =>
    expectExternalIpPoolNotListed(page, externalIpPoolName),
  );

  const poolId = await testStep(
    flow,
    `Create pool "${externalIpPoolName}" with CIDR "${externalIpPoolCidr}"`,
    () => createExternalIpPool(page, { name: externalIpPoolName, cidr: externalIpPoolCidr }),
  );

  // Record ownership before any assertion: if readiness fails, the teardown
  // must still be able to remove the pool this run created.
  await testStep(flow, 'Record the created pool identity for cleanup', () =>
    Promise.resolve(recordCreatedExternalIpPool(poolId)),
  );

  await testStep(flow, 'Verify exactly one create request was sent', () =>
    Promise.resolve(expect(createRequests).toHaveLength(1)),
  );

  await testStep(flow, 'Return to Infrastructure > External IP pools', () =>
    openExternalIpPoolListPage(page),
  );

  await testStep(
    flow,
    `Verify pool "${externalIpPoolName}" reaches ${externalIpPoolReadyStatus}`,
    () => expectExternalIpPoolStatus(page, externalIpPoolName, externalIpPoolReadyStatus),
  );
});

test('refuses to create a pool when the ownership pre-flight lookup fails', async ({ page }) => {
  // Regression: same contract as for the External IP; the pool wizard must not
  // reach its Create request when the pre-flight lookup fails.
  test.setTimeout(300_000);
  const flow = 'external-ip-pool-create-preflight';

  await testStep(flow, 'Log in as the provider admin', () => loginAsProviderAdmin(page));
  await testStep(flow, 'Open Infrastructure > External IP pools', () =>
    openExternalIpPoolListPage(page),
  );

  const createRequests: string[] = [];
  page.on('request', (request) => {
    if (/\/api\/fulfillment\/[\w.]*\.ExternalIPPools\/Create$/.test(request.url())) {
      createRequests.push(request.url());
    }
  });

  await testStep(flow, 'Fail the ownership lookup', () => failExternalIpPoolListRequests(page));
  try {
    await testStep(flow, 'Verify the create is refused before submit', async () => {
      await expect(
        createExternalIpPool(page, { name: externalIpPoolName, cidr: '192.168.100.16/28' }),
      ).rejects.toThrow(/returned HTTP 500/);
      expect(createRequests).toHaveLength(0);
    });
  } finally {
    await restoreExternalIpPoolListRequests(page);
  }
});
