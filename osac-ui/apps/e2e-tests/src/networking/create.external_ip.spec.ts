import { test } from '@playwright/test';

import { loginAsTenantAdmin, resolveTenantAdminIdentity } from './identity';
import {
  externalIpAllocatedStatus,
  externalIpName,
  externalIpPoolName,
  externalIpProject,
} from './test_data';
import {
  createExternalIp,
  expectExternalIpCreateValidation,
  expectExternalIpHasNoEditAction,
  expectExternalIpListed,
  expectExternalIpNotListed,
  expectExternalIpStatus,
  openCreateExternalIpPage,
  openExternalIpListPage,
} from '../../../../libs/test-utils/src/external_ips.page';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';

test('creates an external IP through the tenant wizard', async ({ page }) => {
  // Covers Keycloak login, address allocation and the allocation poll.
  test.setTimeout(600_000);
  const flow = 'external-ip-create';
  const identity = await resolveTenantAdminIdentity();
  logProgress(flow, `Starting flow for "${externalIpName}".`);

  await testStep(flow, 'Log in as the tenant admin', () => loginAsTenantAdmin(page, identity));

  await testStep(flow, 'Open Networking > External IPs', () => openExternalIpListPage(page));

  await testStep(
    flow,
    `Verify external IP "${externalIpName}" does not exist before creation`,
    () => expectExternalIpNotListed(page, externalIpName),
  );

  await testStep(flow, 'Open the create external IP wizard', () => openCreateExternalIpPage(page));

  await testStep(flow, 'Verify the wizard blocks an empty General step', () =>
    expectExternalIpCreateValidation(page),
  );

  await testStep(
    flow,
    `Create external IP "${externalIpName}" from pool "${externalIpPoolName}"`,
    () =>
      createExternalIp(page, {
        name: externalIpName,
        poolName: externalIpPoolName,
        project: externalIpProject || undefined,
      }),
  );

  await testStep(flow, `Verify external IP "${externalIpName}" is listed with an address`, () =>
    expectExternalIpListed(page, externalIpName, externalIpProject || undefined),
  );

  await testStep(
    flow,
    `Verify external IP "${externalIpName}" reaches ${externalIpAllocatedStatus}`,
    () => expectExternalIpStatus(page, externalIpName, externalIpAllocatedStatus),
  );

  await testStep(flow, 'Verify the row offers deletion only, with no edit action', () =>
    expectExternalIpHasNoEditAction(page, externalIpName),
  );
});
