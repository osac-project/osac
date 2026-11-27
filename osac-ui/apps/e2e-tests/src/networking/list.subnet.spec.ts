import { test } from '@playwright/test';

import { loginAsTenantAdmin, resolveTenantAdminIdentity } from './identity';
import { createdSubnetNetworkName } from './subnet_run_state';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
import {
  expectRowsInVirtualNetwork,
  expectSubnetListLoadError,
  expectSubnetSearchFilteredEmpty,
  expectSubnetsListColumns,
  expectVirtualNetworkFilterPersisted,
  filterByVirtualNetwork,
  networkNameFromOptionLabel,
  openSubnetsListPage,
  restoreSubnetListRequests,
  waitForSubnetsList,
} from '../../../../libs/test-utils/src/subnets.page';

test('lists subnets with the documented columns', async ({ page }) => {
  test.setTimeout(300_000);
  const flow = 'subnet-list';
  const identity = await resolveTenantAdminIdentity();
  logProgress(flow, 'Starting the Subnets list checks.');

  await testStep(flow, 'Log in as the tenant admin', () => loginAsTenantAdmin(page, identity));
  await testStep(flow, 'Open Networking > Subnets', () => openSubnetsListPage(page));

  const state = await testStep(flow, 'Verify the list finishes loading', () =>
    waitForSubnetsList(page),
  );
  test.skip(
    state !== 'table',
    'The deployment lists no subnets, so the table columns cannot be asserted. ' +
      'Run create.subnet.spec.ts first.',
  );

  await testStep(flow, 'Verify Name, Status, IPv4 CIDR, Virtual network and Project columns', () =>
    expectSubnetsListColumns(page),
  );

  await testStep(flow, 'Verify an unmatched search reports the filtered empty state', () =>
    expectSubnetSearchFilteredEmpty(page, `no-such-subnet-${process.pid}-${Date.now()}`),
  );
});

test('filters by virtual network from the URL', async ({ page }) => {
  test.setTimeout(300_000);
  const flow = 'subnet-list-filter';
  const identity = await resolveTenantAdminIdentity();
  const networkName = createdSubnetNetworkName();
  test.skip(
    !networkName,
    'Run create.subnet.spec.ts first: the network this run used is not recorded.',
  );

  await testStep(flow, 'Log in as the tenant admin', () => loginAsTenantAdmin(page, identity));
  await testStep(flow, 'Open Networking > Subnets', () => openSubnetsListPage(page));

  const label = await testStep(flow, `Filter by "${networkName}"`, () =>
    filterByVirtualNetwork(page, networkName as string),
  );
  test.skip(!label, `The deployment lists no virtual network named "${networkName}".`);

  await testStep(flow, 'Verify the rows belong to the filtered network', () =>
    expectRowsInVirtualNetwork(page, networkNameFromOptionLabel(label as string)),
  );

  await testStep(flow, 'Verify the filter survives a reload', () =>
    expectVirtualNetworkFilterPersisted(page, networkNameFromOptionLabel(label as string)),
  );
});

test('reports a service error when the list request fails', async ({ page }) => {
  test.setTimeout(300_000);
  const flow = 'subnet-list-error';
  const identity = await resolveTenantAdminIdentity();

  await testStep(flow, 'Log in as the tenant admin', () => loginAsTenantAdmin(page, identity));

  try {
    await testStep(flow, 'Inject a failing list request and verify the error state', () =>
      expectSubnetListLoadError(page),
    );
  } finally {
    await restoreSubnetListRequests(page);
  }
});
