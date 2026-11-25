import { test } from '@playwright/test';

import { loginAsTenantAdmin, resolveTenantAdminIdentity } from './identity';
import { createdSubnetId } from './subnet_run_state';
import { subnetName } from './subnet_test_data';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
import {
  deleteSubnetIfPresent,
  expectDefaultSubnetDeleteDisabled,
  openSubnetsListPage,
  waitForSubnetsList,
} from '../../../../libs/test-utils/src/subnets.page';

test('disables Delete for a Default subnet', async ({ page }) => {
  test.setTimeout(300_000);
  const flow = 'subnet-delete-guard';
  const identity = await resolveTenantAdminIdentity();

  await testStep(flow, 'Log in as the tenant admin', () => loginAsTenantAdmin(page, identity));
  await testStep(flow, 'Open Networking > Subnets', () => openSubnetsListPage(page));

  const state = await testStep(flow, 'Verify the list finishes loading', () =>
    waitForSubnetsList(page),
  );
  test.skip(state !== 'table', 'The deployment lists no subnet rows to check.');

  const found = await testStep(flow, 'Verify Delete is disabled on a Default row', () =>
    expectDefaultSubnetDeleteDisabled(page),
  );
  test.skip(!found, 'The deployment lists no Default subnet row.');
});

test('deletes the subnet this run created', async ({ page }) => {
  test.setTimeout(300_000);
  const flow = 'subnet-delete';
  const identity = await resolveTenantAdminIdentity();
  const expectedId = createdSubnetId();
  logProgress(flow, `Deleting "${subnetName}"${expectedId ? ` (${expectedId})` : ''}.`);

  await testStep(flow, 'Log in as the tenant admin', () => loginAsTenantAdmin(page, identity));
  await testStep(flow, 'Open Networking > Subnets', () => openSubnetsListPage(page));

  const deleted = await testStep(flow, `Delete subnet "${subnetName}" if this run created it`, () =>
    deleteSubnetIfPresent(page, subnetName, expectedId),
  );
  logProgress(
    flow,
    deleted ? `Subnet "${subnetName}" was deleted.` : `Subnet "${subnetName}" was already absent.`,
  );
});
