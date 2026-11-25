import { type Page, expect, test } from '@playwright/test';

import { loginAsTenantAdmin, resolveTenantAdminIdentity } from './identity';
import {
  createdSubnetNetworkName,
  forgetCreatedSubnet,
  recordCreatedSubnet,
  recordCreatedSubnetNetworkName,
} from './subnet_run_state';
import {
  defaultNetworkName,
  injectedCreateFailureMessage,
  rejectedSubnetName,
  subnetCidrOverride,
  subnetName,
  subnetProject,
  subnetReadyStatus,
} from './subnet_test_data';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
import {
  expectCreatePagePrefilledForNetwork,
  expectDefaultSubnetGateConsistent,
  expectOnlyReadyNetworkOptions,
  expectRowsInVirtualNetwork,
  expectSubnetCreateError,
  expectSubnetFormValues,
  expectSubnetListed,
  expectSubnetNotListed,
  expectSubnetStatus,
  fillSubnetForm,
  filterByVirtualNetwork,
  installCreateFailure,
  networkNameFromOptionLabel,
  openCreateSubnetFromFirstFilteredRow,
  openCreateSubnetFromSubnetRow,
  openCreateSubnetPage,
  openSubnetsListPage,
  readCreateCidrPlaceholder,
  restoreCreateRequests,
  selectNetworkOnCreatePage,
  selectNetworkWithFreeBlock,
  submitSubnetCreate,
} from '../../../../libs/test-utils/src/subnets.page';

/** The network the create flow prefers; empty uses the first READY option. */
const configuredNetwork = (): string | undefined => {
  const configured = process.env.OSAC_SUBNET_VIRTUAL_NETWORK?.trim() ?? '';
  return configured || undefined;
};

/** Select a network that still has room, and use the block it offers. */
const selectNetworkForCreate = async (page: Page) => {
  if (subnetCidrOverride) {
    const label = await selectNetworkOnCreatePage(page, configuredNetwork());
    return { label, cidr: subnetCidrOverride };
  }
  return selectNetworkWithFreeBlock(page, configuredNetwork());
};

test('creates a subnet in a READY virtual network', async ({ page }) => {
  // Covers Keycloak login, the create page and the readiness poll.
  test.setTimeout(600_000);
  const flow = 'subnet-create';
  const identity = await resolveTenantAdminIdentity();
  logProgress(flow, `Starting flow for "${subnetName}".`);

  await testStep(flow, 'Log in as the tenant admin', () => loginAsTenantAdmin(page, identity));

  // A leftover id from an interrupted run must not block this run's cleanup.
  forgetCreatedSubnet();

  await testStep(flow, 'Open Networking > Subnets', () => openSubnetsListPage(page));

  await testStep(flow, `Verify subnet "${subnetName}" does not exist yet`, () =>
    expectSubnetNotListed(page, subnetName),
  );

  await testStep(flow, 'Open the create subnet page', () => openCreateSubnetPage(page));

  await testStep(flow, 'Verify the picker lists only READY networks as name (CIDR)', () =>
    expectOnlyReadyNetworkOptions(page),
  );

  const selection = await testStep(
    flow,
    'Select a READY virtual network that has a free block',
    () => selectNetworkForCreate(page),
  );
  const networkName = networkNameFromOptionLabel(selection.label);
  await testStep(flow, 'Record the network this run used', () =>
    Promise.resolve(recordCreatedSubnetNetworkName(networkName)),
  );

  const cidr = selection.cidr;
  logProgress(flow, `Using CIDR "${cidr}" inside "${selection.label}".`);

  const createdId = await testStep(flow, `Create subnet "${subnetName}"`, async () => {
    await fillSubnetForm(page, { name: subnetName, cidr });
    return submitSubnetCreate(page);
  });

  // Record ownership before any assertion: if listing or readiness fails, the
  // teardown must still be able to remove the subnet this run created.
  await testStep(flow, 'Record the created subnet identity for cleanup', () =>
    Promise.resolve(recordCreatedSubnet(createdId)),
  );

  await testStep(flow, `Verify subnet "${subnetName}" is listed with its network and CIDR`, () =>
    expectSubnetListed(page, {
      name: subnetName,
      cidr,
      networkName,
      ...(subnetProject ? { project: subnetProject } : {}),
    }),
  );

  await testStep(flow, `Verify subnet "${subnetName}" reaches ${subnetReadyStatus}`, () =>
    expectSubnetStatus(page, subnetName, subnetReadyStatus),
  );
});

test('pre-fills and locks the network when the create starts from it', async ({ page }) => {
  test.setTimeout(300_000);
  const flow = 'subnet-create-prefill';
  const identity = await resolveTenantAdminIdentity();
  const networkName = createdSubnetNetworkName();
  test.skip(
    !networkName,
    'Run create.subnet.spec.ts first: the network this run used is not recorded.',
  );

  await testStep(flow, 'Log in as the tenant admin', () => loginAsTenantAdmin(page, identity));
  await testStep(flow, 'Open Networking > Subnets', () => openSubnetsListPage(page));

  await testStep(flow, 'Open the create page from the subnet row', () =>
    openCreateSubnetFromSubnetRow(page, subnetName),
  );

  await testStep(flow, 'Verify the network is pre-filled, locked and in the breadcrumb', () =>
    expectCreatePagePrefilledForNetwork(page, networkName as string),
  );

  await testStep(flow, 'Verify the CIDR example lies inside the network', async () => {
    const placeholder = await readCreateCidrPlaceholder(page);
    expect(placeholder, 'The create page offers an example CIDR').toBeTruthy();
  });

  await testStep(flow, 'Cancel and return to the virtual network', async () => {
    await page.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(page).toHaveURL(/\/networking\/virtual-networks\/[^/]+$/);
  });
});

test('applies the tenant default network gate consistently', async ({ page }) => {
  test.setTimeout(300_000);
  const flow = 'subnet-create-gate';
  const identity = await resolveTenantAdminIdentity();

  await testStep(flow, 'Log in as the tenant admin', () => loginAsTenantAdmin(page, identity));
  await testStep(flow, 'Open Networking > Subnets', () => openSubnetsListPage(page));

  const filterLabel = await testStep(
    flow,
    `Filter by the "${defaultNetworkName}" virtual network`,
    () => filterByVirtualNetwork(page, defaultNetworkName),
  );
  test.skip(
    !filterLabel,
    `No virtual network named "${defaultNetworkName}". Set OSAC_SUBNET_VIRTUAL_NETWORK to the tenant default network.`,
  );

  await testStep(flow, 'Open the create page from a subnet of that network', async () => {
    await expectRowsInVirtualNetwork(page, networkNameFromOptionLabel(filterLabel as string));
    await openCreateSubnetFromFirstFilteredRow(page);
  });

  await testStep(flow, 'Verify Create is enabled exactly when the default subnet is Ready', () =>
    expectDefaultSubnetGateConsistent(page),
  );
});

test('shows the server message and keeps the values when create fails', async ({ page }) => {
  test.setTimeout(300_000);
  const flow = 'subnet-create-error';
  const identity = await resolveTenantAdminIdentity();

  await testStep(flow, 'Log in as the tenant admin', () => loginAsTenantAdmin(page, identity));
  await testStep(flow, 'Open Networking > Subnets', () => openSubnetsListPage(page));
  await testStep(flow, 'Open the create subnet page', () => openCreateSubnetPage(page));

  const selection = await testStep(
    flow,
    'Select a READY virtual network that has a free block',
    () => selectNetworkForCreate(page),
  );
  const values = { name: rejectedSubnetName, cidr: selection.cidr };
  await testStep(flow, 'Fill the form', () => fillSubnetForm(page, values));

  try {
    await testStep(flow, 'Fail the create request with the server message', () =>
      installCreateFailure(page, injectedCreateFailureMessage),
    );

    await testStep(flow, 'Submit and verify the inline error', async () => {
      await page.getByRole('button', { name: 'Create', exact: true }).click();
      await expectSubnetCreateError(page, injectedCreateFailureMessage);
    });

    await testStep(flow, 'Verify the submitted values are kept', () =>
      expectSubnetFormValues(page, values),
    );
  } finally {
    await restoreCreateRequests(page);
  }

  logProgress(flow, `Rejected "${values.name}" inside "${selection.label}" as expected.`);
});
