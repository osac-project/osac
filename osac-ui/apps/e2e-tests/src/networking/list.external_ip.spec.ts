import { test } from '@playwright/test';

import { loginAsTenantAdmin, resolveTenantAdminIdentity } from './identity';
import { externalIpName, externalIpProject } from './test_data';
import {
  expectExternalIpListColumns,
  expectExternalIpListLoadError,
  expectExternalIpListed,
  expectExternalIpSearchHasNoMatch,
  openExternalIpListPage,
} from '../../../../libs/test-utils/src/external_ips.page';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';

test('lists external IPs with the documented columns', async ({ page }) => {
  test.setTimeout(300_000);
  const flow = 'external-ip-list';
  const identity = await resolveTenantAdminIdentity();
  logProgress(flow, 'Starting the External IP list checks.');

  await testStep(flow, 'Log in as the tenant admin', () => loginAsTenantAdmin(page, identity));

  await testStep(flow, 'Open Networking > External IPs', () => openExternalIpListPage(page));

  // The create flow owns the row; a missing row means the ordering or the
  // create flow is broken, not that the columns changed.
  await testStep(flow, `Verify external IP "${externalIpName}" is listed`, () =>
    expectExternalIpListed(page, externalIpName, externalIpProject || undefined),
  );

  await testStep(
    flow,
    'Verify Name, Status, Project, Attached to, Address and IP pool columns',
    () => expectExternalIpListColumns(page),
  );

  await testStep(flow, 'Verify an unmatched search reports the filtered empty state', () =>
    expectExternalIpSearchHasNoMatch(page, `no-such-external-ip-${process.pid}-${Date.now()}`),
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
