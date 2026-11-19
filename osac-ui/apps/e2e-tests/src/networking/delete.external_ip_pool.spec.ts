import { test } from '@playwright/test';

import { loginAsProviderAdmin } from './identity';
import { externalIpPoolName, standalonePoolCleanupSkipReason } from './test_data';
import {
  deleteExternalIpPoolIfPresent,
  openExternalIpPoolListPage,
} from '../../../../libs/test-utils/src/external_ips.page';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';

test('deletes the external IP pool the networking suite created', async ({ page }) => {
  test.setTimeout(900_000);
  const flow = 'external-ip-pool-delete';

  // This project exists for manual cleanup after an interrupted run. The gate
  // keeps an unfiltered `playwright test` run from deleting the pool while the
  // External IP flows are still allocating from it.
  const skipReason = standalonePoolCleanupSkipReason();
  if (skipReason) {
    logProgress(flow, `Skipped: ${skipReason}`);
    return;
  }
  logProgress(flow, `Starting flow for "${externalIpPoolName}".`);

  await testStep(flow, 'Log in as the provider admin', () => loginAsProviderAdmin(page));

  await testStep(flow, 'Open Infrastructure > External IP pools', () =>
    openExternalIpPoolListPage(page),
  );

  const deleted = await testStep(
    flow,
    `Delete pool "${externalIpPoolName}" when it is still present`,
    () => deleteExternalIpPoolIfPresent(page, externalIpPoolName),
  );

  logProgress(
    flow,
    deleted
      ? `Deleted pool "${externalIpPoolName}".`
      : `Pool "${externalIpPoolName}" was absent or retained.`,
  );
});
