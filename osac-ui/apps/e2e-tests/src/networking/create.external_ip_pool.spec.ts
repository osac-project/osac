import { test } from '@playwright/test';

import { loginAsProviderAdmin } from './identity';
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
  openExternalIpPoolListPage,
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

  await testStep(flow, 'Log in as the provider admin', () => loginAsProviderAdmin(page));

  await testStep(flow, 'Open Infrastructure > External IP pools', () =>
    openExternalIpPoolListPage(page),
  );

  await testStep(flow, `Verify pool "${externalIpPoolName}" does not exist before creation`, () =>
    expectExternalIpPoolNotListed(page, externalIpPoolName),
  );

  await testStep(
    flow,
    `Create pool "${externalIpPoolName}" with CIDR "${externalIpPoolCidr}"`,
    () => createExternalIpPool(page, { name: externalIpPoolName, cidr: externalIpPoolCidr }),
  );

  await testStep(
    flow,
    `Verify pool "${externalIpPoolName}" reaches ${externalIpPoolReadyStatus}`,
    () => expectExternalIpPoolStatus(page, externalIpPoolName, externalIpPoolReadyStatus),
  );
});
