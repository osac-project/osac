import { expect, test } from '@playwright/test';

import { loginAsTenantAdmin, resolveTenantAdminIdentity } from './identity';
import { forgetCreatedExternalIp, recordCreatedExternalIp } from './run_state';
import {
  externalIpAllocatedStatus,
  externalIpName,
  externalIpPoolName,
  externalIpProject,
} from './test_data';
import {
  createExternalIp,
  expectExternalIpCreateValidation,
  expectExternalIpHasAddress,
  expectExternalIpHasNoEditAction,
  expectExternalIpListed,
  expectExternalIpNotListed,
  expectExternalIpStatus,
  failExternalIpListRequests,
  openCreateExternalIpPage,
  openExternalIpListPage,
  restoreExternalIpListRequests,
} from '../../../../libs/test-utils/src/external_ips.page';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';

test('creates an external IP through the tenant wizard', async ({ page }) => {
  // Covers Keycloak login, address allocation and the allocation poll.
  test.setTimeout(600_000);
  const flow = 'external-ip-create';
  const identity = await resolveTenantAdminIdentity();
  logProgress(flow, `Starting flow for "${externalIpName}".`);

  const createRequests: string[] = [];
  page.on('request', (request) => {
    if (/\/api\/fulfillment\/[\w.]*\.ExternalIPs\/Create$/.test(request.url())) {
      createRequests.push(request.url());
    }
  });

  await testStep(flow, 'Log in as the tenant admin', () => loginAsTenantAdmin(page, identity));

  // Keep a recorded id for an External IP an interrupted run created, so this
  // run's failed precondition does not strip the ownership cleanup needs.
  forgetCreatedExternalIp();

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

  const ipId = await testStep(
    flow,
    `Create external IP "${externalIpName}" from pool "${externalIpPoolName}"`,
    () =>
      createExternalIp(page, {
        name: externalIpName,
        poolName: externalIpPoolName,
        project: externalIpProject || undefined,
      }),
  );

  // Record ownership before any assertion: if listing or allocation fails, the
  // teardown must still be able to remove the External IP this run created.
  await testStep(flow, 'Record the created External IP identity for cleanup', () =>
    Promise.resolve(recordCreatedExternalIp(ipId)),
  );

  await testStep(flow, 'Verify exactly one create request was sent', () =>
    Promise.resolve(expect(createRequests).toHaveLength(1)),
  );

  await testStep(flow, `Verify external IP "${externalIpName}" is listed with an address`, () =>
    expectExternalIpListed(page, externalIpName, externalIpProject || undefined),
  );

  await testStep(
    flow,
    `Verify external IP "${externalIpName}" reaches ${externalIpAllocatedStatus}`,
    () => expectExternalIpStatus(page, externalIpName, externalIpAllocatedStatus),
  );

  await testStep(flow, 'Verify the row shows an allocated address', () =>
    expectExternalIpHasAddress(page, externalIpName),
  );

  await testStep(flow, 'Verify the row offers deletion only, with no edit action', () =>
    expectExternalIpHasNoEditAction(page, externalIpName),
  );
});

test('refuses to create when the ownership pre-flight lookup fails', async ({ page }) => {
  // Regression: a failed pre-flight lookup must stop the run before submit, so
  // no Create request is sent and nothing can be claimed.
  test.setTimeout(300_000);
  const flow = 'external-ip-create-preflight';
  const identity = await resolveTenantAdminIdentity();

  await testStep(flow, 'Log in as the tenant admin', () => loginAsTenantAdmin(page, identity));
  await testStep(flow, 'Open Networking > External IPs', () => openExternalIpListPage(page));
  await testStep(flow, 'Open the create external IP wizard', () => openCreateExternalIpPage(page));

  const createRequests: string[] = [];
  page.on('request', (request) => {
    if (/\/api\/fulfillment\/[\w.]*\.ExternalIPs\/Create$/.test(request.url())) {
      createRequests.push(request.url());
    }
  });

  await testStep(flow, 'Fail the ownership lookup', () => failExternalIpListRequests(page));
  try {
    await testStep(flow, 'Verify the create is refused before submit', async () => {
      await expect(
        createExternalIp(page, { name: externalIpName, poolName: externalIpPoolName }),
      ).rejects.toThrow(/returned HTTP 500/);
      expect(createRequests).toHaveLength(0);
    });
  } finally {
    await restoreExternalIpListRequests(page);
  }
});
