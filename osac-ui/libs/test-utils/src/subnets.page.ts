import { type Page, expect } from '@playwright/test';

import { logProgress } from './general/test_progress';
import { openSubnetsPage } from './main.page';
import { SubnetCreatePage, SubnetDeleteDialog, SubnetsPage } from '../../locators/subnets';

export interface SubnetListExpectation {
  name: string;
  status?: string;
  cidr?: string;
  networkName?: string;
  project?: string;
}

const subnetTableColumns = ['Name', 'Status', 'IPv4 CIDR', 'Virtual network', 'Project'] as const;

const subnetListRoute = /\/api\/fulfillment\/[\w.]*\.Subnets\/List$/;
const subnetCreateRoute = /\/api\/fulfillment\/[\w.]*\.Subnets\/Create$/;
const subnetDeleteRoute = /\/api\/fulfillment\/[\w.]*\.Subnets\/Delete$/;
const subnetListUrl = '/api/fulfillment/osac.public.v1.Subnets/List';
const subnetGetUrl = '/api/fulfillment/osac.public.v1.Subnets/Get';

const escapeRegExp = (value: string): string => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

const subnetsTable = (page: Page) => page.getByRole(...SubnetsPage.subnetsTable);

const subnetRow = (page: Page, name: string) =>
  subnetsTable(page)
    .getByRole('row')
    .filter({
      has: page
        .getByRole('gridcell', { name, exact: true })
        .and(page.locator('td[data-label="Name"]')),
    });

const rowActionsToggle = (page: Page, name: string) =>
  page.getByRole('button', { name: `Actions for ${name}`, exact: true });

const networkOptionPattern = (networkName: string) =>
  new RegExp(`^${escapeRegExp(networkName)} \\(`);

/** Open the Subnets list page through the Networking navigation group. */
export const openSubnetsListPage = async (page: Page): Promise<void> => {
  await openSubnetsPage(page);
  await expect(page.getByRole(...SubnetsPage.pageTitle)).toBeVisible();
};

type SubnetsListState = 'empty' | 'error' | 'table' | 'loading';

const readSubnetsListState = async (page: Page): Promise<SubnetsListState> => {
  if ((await page.getByRole(...SubnetsPage.loadErrorTitle).count()) > 0) {
    return 'error';
  }
  if ((await subnetsTable(page).count()) > 0) {
    return 'table';
  }
  if (
    (await page.getByText(...SubnetsPage.emptyStateTitle).count()) > 0 ||
    (await page.getByText(...SubnetsPage.filteredEmptyStateTitle).count()) > 0
  ) {
    return 'empty';
  }
  return 'loading';
};

/** Wait until the list settles on rows, an empty state or the error state. */
export const waitForSubnetsList = async (page: Page): Promise<SubnetsListState> => {
  await expect.poll(() => readSubnetsListState(page), { timeout: 60_000 }).not.toBe('loading');
  return readSubnetsListState(page);
};

/** Verify the documented columns of the Subnets table. */
export const expectSubnetsListColumns = async (page: Page): Promise<void> => {
  const table = subnetsTable(page);
  for (const column of subnetTableColumns) {
    await expect(table.getByRole('columnheader', { name: column, exact: true })).toBeVisible();
  }
};

/** Verify a subnet row, optionally including its status, CIDR, network and project. */
export const expectSubnetListed = async (
  page: Page,
  expected: SubnetListExpectation,
): Promise<void> => {
  const row = subnetRow(page, expected.name);
  await expect(row).toBeVisible({ timeout: 60_000 });

  if (expected.status) {
    await expect(row.locator('td[data-label="Status"]')).toContainText(expected.status);
  }
  if (expected.cidr) {
    await expect(row.locator('td[data-label="IPv4 CIDR"]')).toHaveText(expected.cidr);
  }
  if (expected.networkName) {
    await expect(
      row.getByRole('link', { name: expected.networkName, exact: true }),
    ).toHaveAttribute('href', /\/networking\/virtual-networks\/[^/]+$/);
  }
  if (expected.project) {
    await expect(row.locator('td[data-label="Project"]')).toContainText(expected.project);
  }
};

/** Verify the name search reports the filtered empty state. */
export const expectSubnetSearchFilteredEmpty = async (page: Page, term: string): Promise<void> => {
  await waitForSubnetsList(page);
  await page.getByRole(...SubnetsPage.searchInput).fill(term);
  await expect(page.getByText(...SubnetsPage.filteredEmptyStateTitle)).toBeVisible({
    timeout: 60_000,
  });
};

/** Verify the subnet does not exist yet. */
export const expectSubnetNotListed = async (page: Page, name: string): Promise<void> => {
  await expectSubnetSearchFilteredEmpty(page, name);
};

/** Read the selected option label of the list's virtual-network filter. */
export const readVirtualNetworkFilterLabel = async (page: Page): Promise<string> =>
  (await page.getByRole(...SubnetsPage.virtualNetworkFilter).innerText()).trim();

/**
 * Select a network in the list's virtual-network filter. Returns the option
 * label (`name (CIDR)`), or `undefined` when the deployment has no network with
 * that name.
 */
export const filterByVirtualNetwork = async (
  page: Page,
  networkName: string,
): Promise<string | undefined> => {
  await page.getByRole(...SubnetsPage.virtualNetworkFilter).click();
  const options = page.getByRole('option');
  await expect(options.first()).toBeVisible({ timeout: 30_000 });
  const option = options.filter({ hasText: networkOptionPattern(networkName) });
  if ((await option.count()) === 0) {
    await page.keyboard.press('Escape');
    return undefined;
  }
  const label = (await option.first().innerText()).trim();
  await option.first().click();
  await expect(page).toHaveURL(new RegExp(`[?&]virtualNetwork=`));
  return label;
};

/** The network name part of a `name (CIDR)` option label. */
export const networkNameFromOptionLabel = (label: string): string => {
  const separator = label.lastIndexOf(' (');
  return separator > 0 ? label.slice(0, separator).trim() : label.trim();
};

/** The CIDR part of a `name (CIDR)` option label. */
export const networkCidrFromOptionLabel = (label: string): string | undefined =>
  label.match(/\((\d+\.\d+\.\d+\.\d+\/\d+)\)\s*$/)?.[1];

/** Verify every visible row belongs to the filtered network. */
export const expectRowsInVirtualNetwork = async (
  page: Page,
  networkName: string,
): Promise<void> => {
  const links = subnetsTable(page).locator('td[data-label="Virtual network"] a');
  const count = await links.count();
  expect(count).toBeGreaterThan(0);
  for (let index = 0; index < count; index += 1) {
    await expect(links.nth(index)).toHaveText(networkName);
  }
};

/** Verify the URL-backed filter survives a reload. */
export const expectVirtualNetworkFilterPersisted = async (
  page: Page,
  networkName: string,
): Promise<void> => {
  await page.reload();
  await expect(page).toHaveURL(new RegExp(`[?&]virtualNetwork=`));
  await expect(page.getByRole(...SubnetsPage.virtualNetworkFilter)).toContainText(
    `${networkName} (`,
  );
  await expectRowsInVirtualNetwork(page, networkName);
};

/** Open the create subnet page from the Subnets list. */
export const openCreateSubnetPage = async (page: Page): Promise<void> => {
  await page.getByRole(...SubnetsPage.createSubnet).click();
  await expect(page.getByRole(...SubnetCreatePage.pageTitle)).toBeVisible();
};

/**
 * Reach the create page the way a user does from a network: search for the
 * listed subnet, open its virtual network, then use the network's Subnets card.
 */
export const openCreateSubnetFromSubnetRow = async (
  page: Page,
  existingSubnetName: string,
): Promise<void> => {
  await page.getByRole(...SubnetsPage.searchInput).fill(existingSubnetName);
  const row = subnetRow(page, existingSubnetName);
  await expect(row).toBeVisible({ timeout: 60_000 });
  await row.getByRole('link').first().click();
  await expect(page).toHaveURL(/\/networking\/virtual-networks\/[^/]+$/);
  // The list page still renders for a moment after the URL changes; its Create
  // link has no network in it. Wait for the network page's link, which carries
  // the `virtualNetwork` parameter, before clicking.
  const createLink = page.getByRole('link', { name: 'Create subnet', exact: true });
  await expect(createLink).toHaveAttribute('href', /[?&]virtualNetwork=/);
  await createLink.click();
  await expect(page.getByRole(...SubnetCreatePage.pageTitle)).toBeVisible();
};

/**
 * Open the create page from the first row of the currently filtered list, so a
 * test that filtered by network reaches that network's page.
 */
export const openCreateSubnetFromFirstFilteredRow = async (page: Page): Promise<void> => {
  await subnetsTable(page).getByRole('row').nth(1).getByRole('link').first().click();
  await expect(page).toHaveURL(/\/networking\/virtual-networks\/[^/]+$/);
  const createLink = page.getByRole('link', { name: 'Create subnet', exact: true });
  await expect(createLink).toHaveAttribute('href', /[?&]virtualNetwork=/);
  await createLink.click();
  await expect(page.getByRole(...SubnetCreatePage.pageTitle)).toBeVisible();
};

/** Labels of the create page's READY network options. */
export const readNetworkOptionLabels = async (page: Page): Promise<string[]> => {
  const toggle = page.locator(...SubnetCreatePage.networkToggle);
  await expect(toggle).toBeVisible({ timeout: 60_000 });
  await toggle.click();
  const options = page.getByRole('option');
  await expect(options.first()).toBeVisible({ timeout: 30_000 });
  const labels = (await options.allInnerTexts()).map((label) => label.trim());
  await page.keyboard.press('Escape');
  return labels;
};

/** Select one network option by its exact `name (CIDR)` label. */
const selectNetworkLabel = async (page: Page, label: string): Promise<void> => {
  const toggle = page.locator(...SubnetCreatePage.networkToggle);
  await toggle.click();
  const option = page.getByRole('option', { name: label, exact: true });
  await expect(option).toBeVisible({ timeout: 30_000 });
  await option.click();
  await expect(toggle).toContainText(label);
};

/**
 * Select a READY network on the create page and return its `name (CIDR)`
 * label. A locked picker is left as it is.
 */
export const selectNetworkOnCreatePage = async (
  page: Page,
  networkName?: string,
): Promise<string> => {
  const toggle = page.locator(...SubnetCreatePage.networkToggle);
  await expect(toggle).toBeVisible({ timeout: 60_000 });
  if (await toggle.isDisabled()) {
    return (await toggle.innerText()).trim();
  }

  const labels = await readNetworkOptionLabels(page);
  const label = networkName
    ? labels.find((candidate) => networkNameFromOptionLabel(candidate) === networkName)
    : labels[0];
  if (!label) {
    throw new Error(
      `No READY virtual network named "${networkName}" is listed. ` +
        'Set OSAC_SUBNET_VIRTUAL_NETWORK to a READY network or unset it to use the first option.',
    );
  }
  await selectNetworkLabel(page, label);
  return label;
};

export interface SubnetNetworkSelection {
  label: string;
  cidr: string;
}

/**
 * The create form offers an example only once the network's subnets are
 * known; until then it shows neither a placeholder nor the no-space reason.
 * Wait for one of the two settled states.
 */
export const waitForCidrExampleOrFull = async (
  page: Page,
  networkCidr?: string,
): Promise<string | undefined> => {
  if (networkCidr) {
    // The helper names the selected network's CIDR, so waiting for it also
    // proves the placeholder belongs to this network and not a previous render.
    await expect(
      page.getByText(
        new RegExp(
          `(?:Must be inside|No free block is available inside) ${escapeRegExp(networkCidr)}\\.`,
        ),
      ),
    ).toBeVisible({ timeout: 30_000 });
  }
  let previous: string | undefined;
  let stable: string | undefined;
  await expect
    .poll(
      async () => {
        if ((await page.getByText(/No free block is available inside/).count()) > 0) {
          return 'full';
        }
        const placeholder =
          (await page.locator(...SubnetCreatePage.cidr).getAttribute('placeholder'))?.trim() ||
          undefined;
        // Require the same example twice, so a render that still carries the
        // previous network's state is not read as this network's free block.
        if (placeholder && placeholder === previous) {
          stable = placeholder;
          return 'example';
        }
        previous = placeholder;
        return 'loading';
      },
      { timeout: 30_000, intervals: [300] },
    )
    .not.toBe('loading');
  return stable;
};

/**
 * Select the first READY network (or `preferredName`) whose create page offers
 * a free block, and return its label and example CIDR. A tenant default network
 * can be fully occupied by its default subnet, so picking blindly fails.
 */
export const selectNetworkWithFreeBlock = async (
  page: Page,
  preferredName?: string,
): Promise<SubnetNetworkSelection> => {
  const toggle = page.locator(...SubnetCreatePage.networkToggle);
  await expect(toggle).toBeVisible({ timeout: 60_000 });

  if (await toggle.isDisabled()) {
    const label = (await toggle.innerText()).trim();
    const cidr = await waitForCidrExampleOrFull(page, networkCidrFromOptionLabel(label));
    if (!cidr) {
      throw new Error(
        `No free block is available inside "${label}". Set OSAC_SUBNET_VIRTUAL_NETWORK ` +
          'to a network with free space, or OSAC_SUBNET_CIDR to a free block.',
      );
    }
    return { label, cidr };
  }

  const labels = await readNetworkOptionLabels(page);
  const ordered = preferredName
    ? labels.filter((candidate) => networkNameFromOptionLabel(candidate) === preferredName)
    : labels;
  if (ordered.length === 0) {
    throw new Error(
      `No READY virtual network named "${preferredName}" is listed. ` +
        'Set OSAC_SUBNET_VIRTUAL_NETWORK to a READY network or unset it.',
    );
  }

  for (const label of ordered) {
    await selectNetworkLabel(page, label);
    const cidr = await waitForCidrExampleOrFull(page, networkCidrFromOptionLabel(label));
    if (cidr) {
      return { label, cidr };
    }
  }
  throw new Error(
    `None of the READY virtual networks has a free block for a subnet: ${ordered.join(', ')}. ` +
      'Create a larger network, set OSAC_SUBNET_VIRTUAL_NETWORK to one with free space, ' +
      'or set OSAC_SUBNET_CIDR to a free block.',
  );
};

/** Verify every network option is labelled `name (IPv4 CIDR)`. */
export const expectOnlyReadyNetworkOptions = async (page: Page): Promise<string[]> => {
  const toggle = page.locator(...SubnetCreatePage.networkToggle);
  await expect(toggle).toBeVisible({ timeout: 60_000 });
  await toggle.click();
  const options = page.getByRole('option');
  await expect(options.first()).toBeVisible({ timeout: 30_000 });
  const labels = (await options.allInnerTexts()).map((label) => label.trim());
  await page.keyboard.press('Escape');

  expect(labels.length).toBeGreaterThan(0);
  for (const label of labels) {
    expect(label).toMatch(/^\S.* \(\d+\.\d+\.\d+\.\d+\/\d+\)$/);
  }
  return labels;
};

/** Verify the create page is locked to the network it was opened from. */
export const expectCreatePagePrefilledForNetwork = async (
  page: Page,
  networkName: string,
): Promise<void> => {
  const toggle = page.locator(...SubnetCreatePage.networkToggle);
  await expect(toggle).toBeDisabled();
  await expect(toggle).toContainText(`${networkName} (`);
  await expect(page.getByText(...SubnetCreatePage.onlyReadyHint)).toHaveCount(0);
  // The breadcrumb names the network between Subnets and Create subnet.
  await expect(page.getByRole('button', { name: networkName, exact: true })).toBeVisible();
  await expect(page.getByText(/Must be inside \d+\.\d+\.\d+\.\d+\/\d+\./)).toBeVisible();
};

/** The example the create form uses as the CIDR placeholder. */
export const readCreateCidrPlaceholder = async (page: Page): Promise<string | undefined> =>
  waitForCidrExampleOrFull(page);

/** Fill the subnet name and CIDR. */
export const fillSubnetForm = async (
  page: Page,
  values: { name: string; cidr: string },
): Promise<void> => {
  await page.locator(...SubnetCreatePage.name).fill(values.name);
  await page.locator(...SubnetCreatePage.cidr).fill(values.cidr);
};

/**
 * Submit the form and return the created subnet id. An invalid form blocks the
 * submit before any request, so the field errors are reported instead of
 * waiting for a response that will never come.
 */
export const submitSubnetCreate = async (page: Page): Promise<string> => {
  const responseOutcome = page.waitForResponse(subnetCreateRoute, { timeout: 120_000 }).then(
    (response) => ({ response }),
    (error: unknown) => ({ error }),
  );
  const fieldErrors = page.locator('[id$="-helper-error"]');
  const validationOutcome = fieldErrors
    .first()
    .waitFor({ state: 'visible', timeout: 15_000 })
    .then(
      () => ({ validation: true as const }),
      () => ({ validation: false as const }),
    );

  await page.getByRole(...SubnetCreatePage.create).click();

  const first = await Promise.race([
    responseOutcome.then((outcome) => ({ kind: 'response' as const, outcome })),
    validationOutcome.then((outcome) => ({ kind: 'validation' as const, outcome })),
  ]);

  if (first.kind === 'validation' && first.outcome.validation) {
    const errors = (await fieldErrors.allInnerTexts()).map((text) => text.trim()).filter(Boolean);
    throw new Error(
      `The form rejected the subnet before Create was sent: ${errors.join(' | ') || 'no field error is visible'}`,
    );
  }

  const settled = await responseOutcome;
  if ('error' in settled) {
    throw new Error('Creating the subnet produced no response.');
  }
  if (!settled.response.ok()) {
    const body = await settled.response.text();
    throw new Error(`Creating the subnet failed with HTTP ${settled.response.status()}: ${body}`);
  }
  const body = (await settled.response.json()) as { object?: { id?: string } };
  const id = body.object?.id;
  if (!id) {
    throw new Error('The subnet create response carried no object id.');
  }
  return id;
};

/** Fail every subnet create request with a Connect error carrying `message`. */
export const installCreateFailure = async (page: Page, message: string): Promise<void> => {
  await page.route(subnetCreateRoute, (route) =>
    route.fulfill({
      status: 400,
      contentType: 'application/json',
      body: JSON.stringify({ code: 'invalid_argument', message }),
    }),
  );
};

/** Remove the injected create failure. */
export const restoreCreateRequests = async (page: Page): Promise<void> => {
  await page.unroute(subnetCreateRoute);
};

/** Verify the server message is shown above the form. */
export const expectSubnetCreateError = async (
  page: Page,
  message: string | RegExp,
): Promise<void> => {
  await expect(page.getByText(...SubnetCreatePage.submitError)).toBeVisible({ timeout: 60_000 });
  await expect(page.getByText(message)).toBeVisible();
};

/** Verify the submitted values are still in the form. */
export const expectSubnetFormValues = async (
  page: Page,
  values: { name: string; cidr: string },
): Promise<void> => {
  await expect(page.locator(...SubnetCreatePage.name)).toHaveValue(values.name);
  await expect(page.locator(...SubnetCreatePage.cidr)).toHaveValue(values.cidr);
};

/**
 * Verify the tenant default network's gate is consistent: the inline reason is
 * present exactly when Create is disabled.
 */
export const expectDefaultSubnetGateConsistent = async (page: Page): Promise<void> => {
  const gate = page.getByText(...SubnetCreatePage.defaultSubnetGate);
  const create = page.getByRole(...SubnetCreatePage.create);
  await expect
    .poll(
      async () =>
        (await gate.count()) > 0 === ((await create.getAttribute('aria-disabled')) === 'true'),
      { timeout: 60_000 },
    )
    .toBe(true);
};

/**
 * Wait for the subnet row to report the expected status. Provisioning is
 * asynchronous: the create response returns while the row still reads
 * `Provisioning`, so the poll needs a much longer budget than an assertion.
 */
export const expectSubnetStatus = async (
  page: Page,
  name: string,
  status: string,
  timeoutMs = 300_000,
): Promise<void> => {
  await expect(subnetRow(page, name)).toBeVisible({ timeout: 60_000 });
  await expect(subnetRow(page, name).locator('td[data-label="Status"]')).toContainText(status, {
    timeout: timeoutMs,
  });
};

/** Fail every subnet list request until the route is removed. */
export const failSubnetListRequests = async (page: Page): Promise<void> => {
  await page.route(subnetListRoute, (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ code: 'internal', message: 'Injected E2E list failure' }),
    }),
  );
};

/** Remove the injected list failure. */
export const restoreSubnetListRequests = async (page: Page): Promise<void> => {
  await page.unroute(subnetListRoute);
};

/** Install the failure before the first load and verify the page error state. */
export const expectSubnetListLoadError = async (page: Page): Promise<void> => {
  await failSubnetListRequests(page);
  await openSubnetsListPage(page);
  await expect(page.getByRole(...SubnetsPage.loadErrorTitle)).toBeVisible({ timeout: 60_000 });
};

/**
 * Identify a subnet by name through the service the page itself calls, so the
 * teardown can prove ownership by id and not only by name.
 */
const subnetIdByName = async (
  page: Page,
  name: string,
  timeoutMs = 30_000,
): Promise<string | undefined> => {
  const pageSize = 100;
  const deadline = Date.now() + timeoutMs;
  let offset = 0;

  for (;;) {
    const remaining = deadline - Date.now();
    if (remaining <= 0) {
      throw new Error(`Timed out after ${timeoutMs / 1_000}s while looking up "${name}".`);
    }
    const body = (await page.evaluate(
      async ({ url, limit, start, budget }) => {
        const controller = new AbortController();
        const timer = setTimeout(() => controller.abort(), budget);
        try {
          const response = await fetch(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'Connect-Protocol-Version': '1' },
            body: JSON.stringify({ limit, offset: start }),
            signal: controller.signal,
          });
          if (!response.ok) {
            throw new Error(`${url} returned HTTP ${response.status}`);
          }
          return (await response.json()) as unknown;
        } finally {
          clearTimeout(timer);
        }
      },
      { url: subnetListUrl, limit: pageSize, start: offset, budget: remaining },
    )) as {
      items?: { id?: string; metadata?: { name?: string } }[];
      total?: number;
    };

    const items = body.items ?? [];
    const match = items.find((item) => item.metadata?.name === name);
    if (match) {
      return match.id;
    }
    if (items.length === 0) {
      return undefined;
    }
    offset += items.length;
    if (typeof body.total === 'number' && offset >= body.total) {
      return undefined;
    }
  }
};

/** Whether the recorded subnet still exists, by its id. */
const subnetExistsById = async (page: Page, id: string, timeoutMs = 30_000): Promise<boolean> => {
  const result = await page.evaluate(
    async ({ url, identifier, budget }) => {
      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(), budget);
      try {
        const response = await fetch(url, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'Connect-Protocol-Version': '1' },
          body: JSON.stringify({ id: identifier }),
          signal: controller.signal,
        });
        return { status: response.status, body: await response.text() };
      } finally {
        clearTimeout(timer);
      }
    },
    { url: subnetGetUrl, identifier: id, budget: timeoutMs },
  );
  if (result.status === 404 || /"code"\s*:\s*"not_found"/.test(result.body)) {
    return false;
  }
  if (result.status !== 200) {
    throw new Error(`${subnetGetUrl} returned HTTP ${result.status}`);
  }
  return true;
};

/** The id of the listed subnet with this name, when it is visible. */
export const readSubnetId = (
  page: Page,
  name: string,
  timeoutMs?: number,
): Promise<string | undefined> => subnetIdByName(page, name, timeoutMs);

/** Fail when a Delete request targets a subnet other than the recorded one. */
const withDeleteGuard = async (
  page: Page,
  expectedId: string | undefined,
  action: () => Promise<void>,
): Promise<string | undefined> => {
  if (!expectedId) {
    await action();
    return undefined;
  }
  let blocked: string | undefined;
  await page.route(subnetDeleteRoute, async (route) => {
    let sentId: string | undefined;
    try {
      sentId = (JSON.parse(route.request().postData() ?? '{}') as { id?: string }).id;
    } catch {
      sentId = undefined;
    }
    if (sentId !== expectedId) {
      blocked = `Delete request targeted ${sentId ?? 'no id'}, expected ${expectedId}; the request was blocked.`;
      await route.abort();
      return;
    }
    await route.continue();
  });
  try {
    await action();
  } finally {
    await page.unroute(subnetDeleteRoute);
  }
  return blocked;
};

/** Verify Delete is disabled on a Default subnet row. Returns false when none is listed. */
export const expectDefaultSubnetDeleteDisabled = async (page: Page): Promise<boolean> => {
  const defaultRow = subnetsTable(page)
    .getByRole('row')
    .filter({ has: page.getByText('Default', { exact: true }) })
    .first();
  if ((await defaultRow.count()) === 0) {
    return false;
  }
  await defaultRow.getByRole('button', { name: /^Actions for / }).click();
  await expect(page.getByRole(...SubnetDeleteDialog.deleteMenuItem)).toHaveAttribute(
    'aria-disabled',
    'true',
  );
  await page.keyboard.press('Escape');
  return true;
};

/** Delete a subnet from its list row and wait for the row to disappear. */
export const deleteSubnetIfPresent = async (
  page: Page,
  name: string,
  expectedId?: string,
): Promise<boolean> => {
  await waitForSubnetsList(page);
  await page.getByRole(...SubnetsPage.searchInput).fill(name);
  if ((await subnetRow(page, name).count()) === 0) {
    if (expectedId && (await subnetExistsById(page, expectedId))) {
      throw new Error(
        `Subnet "${name}" exists (${expectedId}) but has no visible row in the filtered ` +
          'list, so it cannot be deleted from here. Delete it through another interface.',
      );
    }
    logProgress('subnet-delete', `Subnet "${name}" is already absent.`);
    return false;
  }

  logProgress('subnet-delete', `Deleting subnet "${name}".`);
  await rowActionsToggle(page, name).click();
  await page.getByRole(...SubnetDeleteDialog.deleteMenuItem).click();

  const dialog = page.locator(...SubnetDeleteDialog.dialog);
  await expect(dialog).toBeVisible();
  await expect(page.locator(...SubnetDeleteDialog.title)).toContainText(`Delete ${name}?`);
  await expect(page.getByText(...SubnetDeleteDialog.warning)).toBeVisible();

  const deleteOutcome = page.waitForResponse(subnetDeleteRoute, { timeout: 60_000 }).then(
    (response) => ({ response }),
    (error: unknown) => ({ error }),
  );

  const blocked = await withDeleteGuard(page, expectedId, async () => {
    await page.getByRole(...SubnetDeleteDialog.confirmDelete).click();
    await Promise.race([
      dialog.waitFor({ state: 'hidden', timeout: 60_000 }),
      page
        .getByText(...SubnetDeleteDialog.deleteError)
        .waitFor({ state: 'visible', timeout: 60_000 }),
    ]).catch(() => {});
  });
  if (blocked) {
    throw new Error(blocked);
  }

  const settled = await deleteOutcome;
  if ('error' in settled) {
    throw new Error(`Deleting subnet "${name}" produced no response; it still exists.`);
  }
  if (!settled.response.ok()) {
    throw new Error(
      `Deleting subnet "${name}" did not succeed ` +
        `(HTTP ${settled.response.status()}); it still exists.`,
    );
  }
  await expect(dialog).toBeHidden({ timeout: 60_000 });
  await expect(subnetRow(page, name)).toHaveCount(0, { timeout: 180_000 });
  if (expectedId && (await subnetExistsById(page, expectedId))) {
    throw new Error(`Subnet "${name}" still exists after a successful delete response.`);
  }
  return true;
};
