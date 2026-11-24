import { expect, test } from '@playwright/test';

import { loginAsTenantAdmin, resolveTenantAdminIdentity } from './identity';
import { externalIpName } from './test_data';
import {
  deleteExternalIpIfPresent,
  expectExternalIpListColumns,
  expectExternalIpListLoadError,
  expectExternalIpSearchFilteredEmpty,
  failExternalIpListRequests,
  openExternalIpListPage,
  restoreExternalIpListRequests,
  waitForExternalIpList,
} from '../../../../libs/test-utils/src/external_ips.page';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';

test('lists external IPs with the documented columns', async ({ page }) => {
  test.setTimeout(300_000);
  const flow = 'external-ip-list';
  const identity = await resolveTenantAdminIdentity();
  logProgress(flow, 'Starting the External IP list checks.');

  await testStep(flow, 'Log in as the tenant admin', () => loginAsTenantAdmin(page, identity));

  await testStep(flow, 'Open Networking > External IPs', () => openExternalIpListPage(page));

  // Any row proves the table shape: a tenant owns at least its default-nat
  // address once a pool is ready, and the create flow adds another.
  await testStep(flow, 'Verify the list finishes loading', () => waitForExternalIpList(page));

  await testStep(
    flow,
    'Verify Name, Status, Project, Attached to, Address and IP pool columns',
    () => expectExternalIpListColumns(page),
  );

  await testStep(flow, 'Verify an unmatched search reports the filtered empty state', () =>
    expectExternalIpSearchFilteredEmpty(page, `no-such-external-ip-${process.pid}-${Date.now()}`),
  );
});

test('reports a service error when the list request fails', async ({ page }) => {
  test.setTimeout(300_000);
  const flow = 'external-ip-list-error';
  const identity = await resolveTenantAdminIdentity();
  logProgress(flow, 'Starting the External IP list error check.');

  await testStep(flow, 'Log in as the tenant admin', () => loginAsTenantAdmin(page, identity));

  await testStep(flow, 'Inject a failing list request and verify the error state', () =>
    expectExternalIpListLoadError(page),
  );
});

test('refuses to report an External IP as absent when the list fails', async ({ page }) => {
  // Regression: a failed list request is not evidence of absence, so cleanup
  // must fail instead of deleting nothing and claiming success.
  test.setTimeout(300_000);
  const flow = 'external-ip-delete-error';
  const identity = await resolveTenantAdminIdentity();
  logProgress(flow, 'Checking that a failed list is not reported as absence.');

  await testStep(flow, 'Log in as the tenant admin', () => loginAsTenantAdmin(page, identity));

  await testStep(flow, 'Inject a failing list request', () => failExternalIpListRequests(page));

  try {
    await testStep(flow, 'Open Networking > External IPs', () => openExternalIpListPage(page));

    await testStep(flow, 'Verify the delete helper refuses to report absence', async () => {
      await expect(deleteExternalIpIfPresent(page, externalIpName)).rejects.toThrow(
        /failed with HTTP 500|failed to load/i,
      );
    });
  } finally {
    await restoreExternalIpListRequests(page);
  }
});
