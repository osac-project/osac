import { test } from '@playwright/test';

import { loginAsProviderAdmin } from './identity';
import { externalIpPoolName, externalIpPoolProvisioningSkipReason } from './test_data';
import {
  deleteExternalIpPoolIfPresent,
  openExternalIpPoolListPage,
} from '../../../../libs/test-utils/src/external_ips.page';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';

test('deletes the external IP pool the networking suite created', async ({ page }) => {
  test.setTimeout(900_000);
  const flow = 'external-ip-pool-delete';
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

  await testStep(flow, `Delete pool "${externalIpPoolName}" when it is still present`, () =>
    deleteExternalIpPoolIfPresent(page, externalIpPoolName),
  );
});
