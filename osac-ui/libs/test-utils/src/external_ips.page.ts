import { type Page, expect } from '@playwright/test';

import { selectOption } from './general/form_controls';
import { logProgress } from './general/test_progress';
import { openExternalIpPoolsPage, openExternalIpsPage } from './main.page';
import {
  ExternalIpCreatePage,
  ExternalIpDeleteDialog,
  ExternalIpPoolCreatePage,
  ExternalIpPoolsPage,
  ExternalIpsPage,
} from '../../locators/external_ips';

export interface ExternalIpCreateParams {
  name: string;
  poolName: string;
  project?: string;
}

export interface ExternalIpPoolCreateParams {
  name: string;
  cidr: string;
}

const externalIpTableColumns = [
  'Name',
  'Status',
  'Project',
  'Attached to',
  'Address',
  'IP pool',
  'Created',
] as const;

/**
 * The External IPs list is a Connect call to the ExternalIPs service (the
 * public one for a tenant session). Matching the service segment keeps
 * session, project and pool lookups untouched so only the list request fails.
 */
const externalIpListRoute = /\/api\/fulfillment\/[\w.]*\.ExternalIPs\/List$/;
const externalIpPoolListRoute = /\/api\/fulfillment\/[\w.]*\.ExternalIPPools\/List$/;

const externalIpTable = (page: Page) => page.getByRole(...ExternalIpsPage.externalIpsTable);

const externalIpPoolTable = (page: Page) => page.getByRole(...ExternalIpPoolsPage.poolsTable);

/**
 * Both list pages default to card view, which renders no table or cells. Table
 * assertions switch to list view; a page in an error state has no switcher, so
 * a missing toggle is not an error.
 */
const selectListView = async (page: Page, buttonId: string): Promise<void> => {
  const toggle = page.locator(`#${buttonId}`);
  if ((await toggle.count()) === 0) {
    return;
  }
  if ((await toggle.getAttribute('aria-pressed')) === 'true') {
    return;
  }
  await toggle.click();
  await expect(toggle).toHaveAttribute('aria-pressed', 'true');
};

/** Switch the External IPs page to list view so its table and cells exist. */
export const selectExternalIpListView = (page: Page): Promise<void> =>
  selectListView(page, 'list-view');

const externalIpRow = (page: Page, name: string) =>
  externalIpTable(page)
    .getByRole('row')
    // The table renders role="grid", so its <td> elements are gridcell, not cell.
    // Restrict the name to the Name column: the IP pool cell can repeat the text.
    .filter({
      has: page
        .getByRole('gridcell', { name, exact: true })
        .and(page.locator('td[data-label="Name"]')),
    });

const externalIpPoolRow = (page: Page, name: string) =>
  externalIpPoolTable(page)
    .getByRole('row')
    .filter({ has: page.getByRole('link', { name, exact: true }) });

const rowActionsToggle = (page: Page, name: string) =>
  page.getByRole('button', { name: `Actions for ${name}`, exact: true });

/**
 * The provider's own wording for the one refusal that is a documented outcome:
 * the pool still has an allocated address. Any other delete error must fail.
 */
const allocatedAddressRefusal = /active ExternalIP resource\(s\) still reference it/i;

/** Open the External IPs list page through the Networking navigation group. */
export const openExternalIpListPage = async (page: Page): Promise<void> => {
  await openExternalIpsPage(page);
  await expect(page.getByRole(...ExternalIpsPage.pageTitle)).toBeVisible();
};

/** Open the admin External IP pools page through the Infrastructure group. */
export const openExternalIpPoolListPage = async (page: Page): Promise<void> => {
  await openExternalIpPoolsPage(page);
  await expect(page.getByRole(...ExternalIpPoolsPage.pageTitle)).toBeVisible();
};

type ExternalIpListState = 'empty' | 'error' | 'table';

const readExternalIpListState = async (page: Page): Promise<ExternalIpListState | 'loading'> => {
  if ((await page.getByRole(...ExternalIpsPage.loadErrorTitle).count()) > 0) {
    return 'error';
  }
  if ((await externalIpTable(page).count()) > 0) {
    return 'table';
  }
  // A search that matches nothing replaces the table with the filtered title;
  // either empty state means "settled with no rows".
  if (
    (await page.getByText(...ExternalIpsPage.emptyStateTitle).count()) > 0 ||
    (await page.getByText(...ExternalIpsPage.filteredEmptyStateTitle).count()) > 0
  ) {
    return 'empty';
  }
  return 'loading';
};

/**
 * Wait until the list settles on rows, an empty state, or an error, and report
 * which. Callers that need "this resource is absent" must reject the error
 * state: a failed list is not evidence of absence.
 */
export const waitForExternalIpList = async (page: Page): Promise<ExternalIpListState> => {
  await selectExternalIpListView(page);
  let settled: ExternalIpListState | 'loading' = 'loading';
  await expect
    .poll(
      async () => {
        settled = await readExternalIpListState(page);
        return settled;
      },
      { timeout: 30_000, message: 'The External IP list did not finish loading.' },
    )
    .not.toBe('loading');
  return settled === 'loading' ? 'empty' : settled;
};

/** Verify the list renders the documented columns. */
export const expectExternalIpListColumns = async (page: Page): Promise<void> => {
  await waitForExternalIpList(page);
  if ((await externalIpTable(page).count()) === 0) {
    throw new Error('The External IP list is empty, so its columns cannot be verified.');
  }
  for (const column of externalIpTableColumns) {
    await expect(
      externalIpTable(page).getByRole('columnheader', { name: column, exact: true }),
    ).toBeVisible();
  }
};

/** Verify an External IP row exists and, when supplied, its Project cell. */
export const expectExternalIpListed = async (
  page: Page,
  name: string,
  projectLabel?: string,
): Promise<void> => {
  await waitForExternalIpList(page);
  const row = externalIpRow(page, name);
  await expect(row).toHaveCount(1, { timeout: 30_000 });
  if (projectLabel) {
    await expect(row.locator('td[data-label="Project"]')).toHaveText(projectLabel);
  }
};

/**
 * Wait, bounded, for a named row to exist. The list response can lag a just
 * created resource, so a single immediate count is not a reliable absence
 * signal.
 */
const waitForExternalIpRow = async (
  page: Page,
  name: string,
  timeout = 15_000,
): Promise<boolean> => {
  const row = externalIpRow(page, name);
  const listFailed = (when: string): Error =>
    new Error(
      `The External IP list failed ${when}, so the presence of "${name}" cannot be ` +
        'verified. Nothing was deleted; re-run once the list request succeeds.',
    );

  const settled = await waitForExternalIpList(page);
  if (settled === 'error') {
    throw listFailed('to load');
  }
  // Poll the row count directly: unlike an assertion, this surfaces any
  // unexpected page error instead of catching it as a possible absence.
  const deadline = Date.now() + timeout;
  for (;;) {
    const matches = await row.count();
    if (matches === 1) {
      return true;
    }
    if (matches > 1) {
      throw new Error(`External IP "${name}" matches ${matches} rows; expected exactly one.`);
    }
    if (Date.now() >= deadline) {
      break;
    }
    await page.waitForTimeout(250);
  }

  const afterWait = await waitForExternalIpList(page);
  if (afterWait === 'error') {
    throw listFailed('while checking for the row');
  }
  const matches = await row.count();
  if (matches > 1) {
    throw new Error(`External IP "${name}" matches ${matches} rows; expected exactly one.`);
  }
  return matches === 1;
};

/** Verify an External IP is absent, failing with a duplicate-resource message. */
export const expectExternalIpNotListed = async (page: Page, name: string): Promise<void> => {
  const listState = await waitForExternalIpList(page);
  if (listState === 'error') {
    throw new Error(
      'The External IP list failed to load, so absence before creation cannot be verified.',
    );
  }
  const row = externalIpRow(page, name);
  const matchingRows = await row.count();
  if (matchingRows > 0) {
    throw new Error(
      `External IP "${name}" already exists (${matchingRows} matching rows). ` +
        'Delete it, or run with a different OSAC_E2E_INSTANCE.',
    );
  }
};

/** Verify the search filter reports no match for the supplied term. */
/**
 * Search for a term and verify the named External IP is not listed. The search
 * is debounced and keeps previous data until it applies, so the check polls:
 * the target must not appear, and the filtered-empty state ends it early.
 */
export const expectExternalIpSearchHasNoMatch = async (
  page: Page,
  term: string,
  timeoutMs = 15_000,
): Promise<void> => {
  await page.getByRole(...ExternalIpsPage.searchInput).fill(term);
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if ((await externalIpRow(page, term).count()) > 0) {
      throw new Error(
        `External IP "${term}" is visible in this session; expected it to be hidden.`,
      );
    }
    if ((await page.getByText(...ExternalIpsPage.filteredEmptyStateTitle).count()) > 0) {
      return;
    }
    if (Date.now() >= deadline) {
      if ((await page.getByRole(...ExternalIpsPage.loadErrorTitle).count()) > 0) {
        throw new Error(`The External IP search for "${term}" left the list in its error state.`);
      }
      const settled =
        (await externalIpTable(page).count()) > 0 ||
        (await page.getByText(...ExternalIpsPage.emptyStateTitle).count()) > 0;
      if (!settled) {
        throw new Error(`The External IP search for "${term}" did not settle in time.`);
      }
      return;
    }
    await page.waitForTimeout(250);
  }
};

/** Verify the search renders the filtered empty state for an unmatched term. */
export const expectExternalIpSearchFilteredEmpty = async (
  page: Page,
  term: string,
  timeoutMs = 15_000,
): Promise<void> => {
  await page.getByRole(...ExternalIpsPage.searchInput).fill(term);
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if ((await page.getByText(...ExternalIpsPage.filteredEmptyStateTitle).count()) > 0) {
      return;
    }
    if (Date.now() >= deadline) {
      throw new Error(
        `The filtered empty state did not render for "${term}" within ${timeoutMs / 1_000} seconds.`,
      );
    }
    await page.waitForTimeout(250);
  }
};

/**
 * Read a resource id by name through the same API the page uses. Cleanup needs
 * identity, not just a name, so it never deletes a resource this run did not
 * create.
 */
const resourceIdByName = async (
  page: Page,
  path: string,
  name: string,
  timeoutMs = 30_000,
): Promise<string | undefined> => {
  // The list is paged; an omitted limit does not guarantee every match, so walk
  // it until the resource is found or the reported total is consumed. The
  // budget bounds each request too: a stalled response must not outlive the
  // caller's deadline and turn into a test timeout.
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
      { url: path, limit: pageSize, start: offset, budget: remaining },
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

/** Whether a resource id still exists, via the service's Get call. */
const resourceExistsById = async (
  page: Page,
  path: string,
  id: string,
  timeoutMs = 30_000,
): Promise<boolean> => {
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
    { url: path, identifier: id, budget: timeoutMs },
  );
  if (result.status === 404 || /"code"\s*:\s*"not_found"/.test(result.body)) {
    return false;
  }
  if (result.status !== 200) {
    throw new Error(`${path} returned HTTP ${result.status}`);
  }
  return true;
};

/** Whether the recorded External IP still exists, by its id. */
export const externalIpExistsById = (page: Page, id: string): Promise<boolean> =>
  resourceExistsById(page, '/api/fulfillment/osac.public.v1.ExternalIPs/Get', id);

/** Whether the recorded pool still exists, by its id. */
export const externalIpPoolExistsById = (page: Page, id: string): Promise<boolean> =>
  resourceExistsById(page, '/api/fulfillment/osac.private.v1.ExternalIPPools/Get', id);

/** Identify the created External IP, so cleanup can prove ownership. */
export const readExternalIpId = (
  page: Page,
  name: string,
  timeoutMs?: number,
): Promise<string | undefined> =>
  resourceIdByName(page, '/api/fulfillment/osac.public.v1.ExternalIPs/List', name, timeoutMs);

/** Identify the created External IP pool, so cleanup can prove ownership. */
export const readExternalIpPoolId = (
  page: Page,
  name: string,
  timeoutMs?: number,
): Promise<string | undefined> =>
  resourceIdByName(page, '/api/fulfillment/osac.private.v1.ExternalIPPools/List', name, timeoutMs);

/** Fail every External IP list request until the route is removed. */
export const failExternalIpListRequests = async (page: Page): Promise<void> => {
  await page.route(externalIpListRoute, (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ code: 'internal', message: 'Injected E2E list failure' }),
    }),
  );
};

/** Remove the injected list failure. */
export const restoreExternalIpListRequests = async (page: Page): Promise<void> => {
  await page.unroute(externalIpListRoute);
};

/**
 * Fail the list request and verify the page renders its error state. Install
 * the route before the first list load: a query already in the TanStack cache
 * is not re-issued, so intercepting after a successful load proves nothing.
 */
export const expectExternalIpListLoadError = async (page: Page): Promise<void> => {
  await failExternalIpListRequests(page);
  try {
    await openExternalIpListPage(page);
    await expect(page.getByRole(...ExternalIpsPage.loadErrorTitle)).toBeVisible({
      timeout: 30_000,
    });
  } finally {
    await restoreExternalIpListRequests(page);
  }
};

/** Fail every External IP pool list request until the route is removed. */
export const failExternalIpPoolListRequests = async (page: Page): Promise<void> => {
  await page.route(externalIpPoolListRoute, (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ code: 'internal', message: 'Injected E2E pool list failure' }),
    }),
  );
};

/** Remove the injected pool list failure. */
export const restoreExternalIpPoolListRequests = async (page: Page): Promise<void> => {
  await page.unroute(externalIpPoolListRoute);
};

/** Verify the Address cell shows a real address, not the empty placeholder. */
export const expectExternalIpHasAddress = async (
  page: Page,
  name: string,
  timeoutMs = 30_000,
): Promise<void> => {
  const cell = externalIpRow(page, name).locator('td[data-label="Address"]');
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const remaining = Math.max(1, deadline - Date.now());
    const address = (await cell.innerText({ timeout: remaining }).catch(() => '')).trim();
    if (/^[0-9a-fA-F:.]+$/.test(address)) {
      return;
    }
    if (Date.now() >= deadline) {
      throw new Error(
        `External IP "${name}" did not show an address within ${timeoutMs / 1_000} seconds ` +
          `(the Address cell reads "${address || 'empty'}").`,
      );
    }
    await page.waitForTimeout(250);
  }
};

/** Verify status reaches the expected value, reporting the observed row on timeout. */
export const expectExternalIpStatus = async (
  page: Page,
  name: string,
  status: string,
  timeout = 180_000,
): Promise<void> => {
  const row = externalIpRow(page, name);
  try {
    await expect(row.getByText(status, { exact: true })).toBeVisible({ timeout });
  } catch {
    const observed = (
      await row
        .first()
        .innerText({ timeout: 3_000 })
        .catch(() => '')
    )
      .replace(/\s+/g, ' ')
      .trim();
    throw new Error(
      `External IP "${name}" did not reach status "${status}" within ${timeout / 1_000} seconds. ` +
        `The row currently reads: "${observed || 'unknown'}".`,
    );
  }
};

/** Open the creation wizard from the list page. */
export const openCreateExternalIpPage = async (page: Page): Promise<void> => {
  const createLink = page.getByRole(...ExternalIpsPage.createExternalIp);
  await expect(createLink).toBeVisible();
  await createLink.click();
  await expect(page.getByRole(...ExternalIpCreatePage.pageTitle)).toBeVisible();
};

/** Press Next on the current wizard step. */
const goToNextWizardStep = async (page: Page): Promise<void> => {
  await page.getByRole(...ExternalIpCreatePage.next).click();
};

/** Continue past the General step after entering the requested name and project. */
const submitGeneralStep = async (page: Page, params: ExternalIpCreateParams): Promise<void> => {
  await expect(page.getByRole(...ExternalIpCreatePage.generalStepTitle)).toBeVisible();
  await page.locator(...ExternalIpCreatePage.name).fill(params.name);
  if (params.project) {
    await selectOption(page, ExternalIpCreatePage.project, params.project, 0, true);
  }
  await goToNextWizardStep(page);
  await expect(page.getByRole(...ExternalIpCreatePage.configurationStepTitle)).toBeVisible();
};

/** Select the external IP pool, unless auto-selection already did. */
const submitConfigurationStep = async (page: Page, poolName: string): Promise<void> => {
  const toggle = page.locator(...ExternalIpCreatePage.pool);
  const selectedPool = (await toggle.innerText()).trim();
  if (selectedPool !== poolName) {
    await selectOption(page, ExternalIpCreatePage.pool, poolName, 0, true);
  }
  await goToNextWizardStep(page);
  await expect(page.getByRole(...ExternalIpCreatePage.reviewStepTitle)).toBeVisible();
};

/** Create an External IP through the wizard and land back on the list page. */
/**
 * Read a create response body within the same deadline as its receipt. A body
 * that stalls would otherwise wait until the test timeout.
 */
const readResponseJson = async <T>(
  response: { json: () => Promise<unknown> },
  description: string,
  deadline: number,
): Promise<T> => {
  const remaining = deadline - Date.now();
  if (remaining <= 0) {
    throw new Error(`Reading the create response for ${description} exceeded its deadline.`);
  }
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return (await Promise.race([
      response.json() as Promise<T>,
      new Promise<never>((_resolve, reject) => {
        timer = setTimeout(
          () => reject(new Error(`Reading the create response for ${description} timed out.`)),
          remaining,
        );
      }),
    ])) as T;
  } finally {
    if (timer) {
      clearTimeout(timer);
    }
  }
};

export const createExternalIp = async (
  page: Page,
  params: ExternalIpCreateParams,
): Promise<string> => {
  logProgress('external-ip-create', `Creating external IP "${params.name}".`);
  await submitGeneralStep(page, params);
  await submitConfigurationStep(page, params.poolName);

  const review = page.locator(...ExternalIpCreatePage.reviewDetails);
  await expect(review).toContainText(params.name);
  await expect(review).toContainText(params.poolName);

  // Never claim a resource this run did not create: if the name already exists,
  // the submit would fail and the post-submit lookup would return its id.
  if (await readExternalIpId(page, params.name)) {
    throw new Error(
      `External IP "${params.name}" already exists before creation; this run cannot claim it.`,
    );
  }

  // Bind the recorded id to the creation response, not to a name lookup: a
  // failed lookup must not be read as "the name is free".
  const deadline = Date.now() + 60_000;
  const responseOutcome = page
    .waitForResponse(
      (response) => /\/api\/fulfillment\/[\w.]*\.ExternalIPs\/Create$/.test(response.url()),
      { timeout: 60_000 },
    )
    .then(
      (response) => ({ response }),
      (error: unknown) => ({ error }),
    );
  // Observe both operations from the start: if the click fails, consume the
  // pending response wait so it is not reported as an unhandled rejection.
  const response = await submitAndReadResponse(
    () => page.getByRole(...ExternalIpCreatePage.submit).click(),
    responseOutcome,
    `Creating external IP "${params.name}" produced no response within 60 seconds.`,
  );
  if (!response.ok()) {
    const alert = page
      .locator('[class*="c-alert"]')
      .filter({ hasText: ExternalIpCreatePage.submitError[0] })
      .first();
    const detail = (await alert.innerText({ timeout: 3_000 }).catch(() => ''))
      .replace(/\s+/g, ' ')
      .trim();
    throw new Error(
      `Creating external IP "${params.name}" failed: ${detail || `HTTP ${response.status()}`}. ` +
        'Check that the deployment enables provider networking ' +
        '(global.networking.provisioningEnabled) and that the pool can allocate.',
    );
  }
  const created = await readResponseJson<{ object?: { id?: string } }>(
    response,
    `external IP "${params.name}"`,
    deadline,
  );
  const createdId = created.object?.id;
  if (!createdId) {
    throw new Error(`The create response for "${params.name}" carried no object id.`);
  }
  return createdId;
};

/** Press Next on an untouched form and verify the wizard blocks with an alert. */
export const expectExternalIpCreateValidation = async (page: Page): Promise<void> => {
  await goToNextWizardStep(page);
  await expect(page.getByText(...ExternalIpCreatePage.validationAlert)).toBeVisible();
  await expect(page.getByRole(...ExternalIpCreatePage.generalStepTitle)).toBeVisible();
};

/** Verify the row actions menu offers deletion only, with no edit affordance. */
export const expectExternalIpHasNoEditAction = async (page: Page, name: string): Promise<void> => {
  await rowActionsToggle(page, name).click();
  const menuItems = page.getByRole('menu').getByRole('menuitem');
  await expect(menuItems).toHaveCount(1);
  await expect(page.getByRole(...ExternalIpDeleteDialog.deleteMenuItem)).toBeVisible();
  await expect(page.getByRole('menuitem', { name: /edit/i })).toHaveCount(0);
  await page.keyboard.press('Escape');
};

/**
 * Bind a delete to the resource this run recorded: intercept the outgoing
 * request and block it when it targets a different id.
 */
/**
 * Decide what a missing UI row means. The name search is a `contains` filter
 * over a page-limited list, so an owned resource can exist in the API while it
 * has no visible row. That case must fail loudly instead of reading as absent.
 */
export const missingRowVerdict = (
  expectedId: string | undefined,
  currentId: string | undefined,
): 'absent' | 'invisible' => (expectedId && currentId === expectedId ? 'invisible' : 'absent');

const withDeleteGuard = async (
  page: Page,
  routePattern: RegExp,
  expectedId: string | undefined,
  action: () => Promise<void>,
): Promise<string | undefined> => {
  if (!expectedId) {
    await action();
    return undefined;
  }
  let blocked: string | undefined;
  await page.route(routePattern, async (route) => {
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
    await page.unroute(routePattern);
  }
  return blocked;
};

/**
 * Click the wizard submit and return the Create response, observing both
 * operations from the start. Playwright cannot cancel `waitForResponse`, so a
 * click failure races the pending outcome for a short fixed window and surfaces
 * the click error; the pending wait stays handled and cannot fail the run.
 */
export const submitAndReadResponse = async <T>(
  click: () => Promise<void>,
  responseOutcome: Promise<{ response: T } | { error: unknown }>,
  noResponseMessage: string,
  clickFailureWindowMs = 1_000,
): Promise<T> => {
  let clickError: unknown;
  try {
    await click();
  } catch (error) {
    clickError = error;
  }
  if (clickError) {
    await Promise.race([
      responseOutcome,
      new Promise((resolve) => setTimeout(resolve, clickFailureWindowMs)),
    ]);
    throw clickError instanceof Error ? clickError : new Error('The create submit click failed.');
  }

  const outcome = await responseOutcome;
  if ('error' in outcome) {
    throw new Error(noResponseMessage);
  }
  return outcome.response;
};

/** Delete an External IP from its list row and wait for the row to disappear. */
export const deleteExternalIpIfPresent = async (
  page: Page,
  name: string,
  expectedId?: string,
  options: { rowTimeoutMs?: number } = {},
): Promise<boolean> => {
  await waitForExternalIpList(page);
  // Scope the list to the name before judging the rows; the row wait polls, so
  // a stale empty render cannot be mistaken for absence.
  await page.getByRole(...ExternalIpsPage.searchInput).fill(name);
  if (!(await waitForExternalIpRow(page, name, options.rowTimeoutMs))) {
    // A missing row is only absence when the recorded resource is gone too;
    // the check is by id, so name and page limits cannot mislead it.
    if (expectedId && (await externalIpExistsById(page, expectedId))) {
      throw new Error(
        `External IP "${name}" exists (${expectedId}) but has no visible row in the ` +
          'filtered list, so it cannot be deleted from here. Delete it through another interface.',
      );
    }
    logProgress('external-ip-delete', `External IP "${name}" is already absent.`);
    return false;
  }

  logProgress('external-ip-delete', `Deleting external IP "${name}".`);
  await rowActionsToggle(page, name).click();
  await page.getByRole(...ExternalIpDeleteDialog.deleteMenuItem).click();

  const dialog = page.locator(...ExternalIpDeleteDialog.dialog);
  await expect(dialog).toBeVisible();
  await expect(page.locator(...ExternalIpDeleteDialog.title)).toContainText(`Delete ${name}?`);
  await expect(page.getByText(...ExternalIpDeleteDialog.warning)).toBeVisible();
  const deleteOutcome = page
    .waitForResponse(
      (response) => /\/api\/fulfillment\/[\w.]*\.ExternalIPs\/Delete$/.test(response.url()),
      { timeout: 60_000 },
    )
    .then(
      (response) => ({ response }),
      (error: unknown) => ({ error }),
    );
  const blocked = await withDeleteGuard(
    page,
    /\/api\/fulfillment\/[\w.]*\.ExternalIPs\/Delete$/,
    expectedId,
    async () => {
      await page.getByRole(...ExternalIpDeleteDialog.confirmDelete).click();
      await Promise.race([
        dialog.waitFor({ state: 'hidden', timeout: 60_000 }),
        page
          .getByText(...ExternalIpDeleteDialog.deleteError)
          .waitFor({ state: 'visible', timeout: 60_000 }),
      ]).catch(() => {});
    },
  );
  if (blocked) {
    throw new Error(blocked);
  }
  // Report the HTTP failure before a dialog-still-open failure, so the cause is
  // not hidden by a visibility timeout.
  const settled = await deleteOutcome;
  if ('error' in settled) {
    throw new Error(`Deleting external IP "${name}" produced no response; it is still allocated.`);
  }
  if (!settled.response.ok()) {
    throw new Error(
      `Deleting external IP "${name}" did not succeed ` +
        `(HTTP ${settled.response.status()}); it is still allocated.`,
    );
  }
  await expect(dialog).toBeHidden({ timeout: 60_000 });
  await expect(externalIpRow(page, name)).toHaveCount(0, { timeout: 180_000 });
  if (expectedId && (await externalIpExistsById(page, expectedId))) {
    throw new Error(`External IP "${name}" still exists after a successful delete response.`);
  }
  return true;
};

/** Wait until the pool list settles on rows or an empty state. */
export const waitForExternalIpPoolList = async (page: Page): Promise<void> => {
  await selectListView(page, 'list-view');
  await expect
    .poll(
      async () => {
        if ((await externalIpPoolTable(page).count()) > 0) {
          return 'table';
        }
        if ((await page.getByText(...ExternalIpPoolsPage.emptyStateTitle).count()) > 0) {
          return 'empty';
        }
        return 'loading';
      },
      { timeout: 30_000, message: 'The External IP pool list did not finish loading.' },
    )
    .not.toBe('loading');
};

/** Create an admin-only External IP pool through the Infrastructure wizard. */
export const createExternalIpPool = async (
  page: Page,
  params: ExternalIpPoolCreateParams,
): Promise<string> => {
  logProgress('external-ip-pool-create', `Creating external IP pool "${params.name}".`);
  const createLink = page.getByRole(...ExternalIpPoolsPage.createPool);
  await expect(createLink).toBeVisible();
  await createLink.click();
  await expect(page.getByRole(...ExternalIpPoolCreatePage.pageTitle)).toBeVisible();

  await page.locator(...ExternalIpPoolCreatePage.name).fill(params.name);
  await page.locator(...ExternalIpPoolCreatePage.cidr).fill(params.cidr);
  await page.getByRole(...ExternalIpPoolCreatePage.next).click();
  await expect(page.getByRole(...ExternalIpPoolCreatePage.submit)).toBeVisible();

  // Same guard as for the External IP: an existing name must not be claimed.
  if (await readExternalIpPoolId(page, params.name)) {
    throw new Error(
      `External IP pool "${params.name}" already exists before creation; this run cannot claim it.`,
    );
  }

  // Same binding for the pool: the id comes from the create response.
  const deadline = Date.now() + 60_000;
  const responseOutcome = page
    .waitForResponse(
      (response) => /\/api\/fulfillment\/[\w.]*\.ExternalIPPools\/Create$/.test(response.url()),
      { timeout: 60_000 },
    )
    .then(
      (response) => ({ response }),
      (error: unknown) => ({ error }),
    );
  // Observe both operations from the start: if the click fails, consume the
  // pending response wait so it is not reported as an unhandled rejection.
  const response = await submitAndReadResponse(
    () => page.getByRole(...ExternalIpPoolCreatePage.submit).click(),
    responseOutcome,
    `Creating external IP pool "${params.name}" produced no response within 60 seconds.`,
  );
  if (!response.ok()) {
    const detail = (
      await page
        .locator('[class*="c-alert"]')
        .first()
        .innerText({ timeout: 3_000 })
        .catch(() => '')
    )
      .replace(/\s+/g, ' ')
      .trim();
    throw new Error(
      `Creating external IP pool "${params.name}" failed: ${detail || `HTTP ${response.status()}`}.`,
    );
  }
  const created = await readResponseJson<{ object?: { id?: string } }>(
    response,
    `pool "${params.name}"`,
    deadline,
  );
  const createdId = created.object?.id;
  if (!createdId) {
    throw new Error(`The create response for pool "${params.name}" carried no object id.`);
  }
  return createdId;
};

/** Wait for a pool status, reporting what the row shows on timeout. */
export const expectExternalIpPoolStatus = async (
  page: Page,
  name: string,
  status: string,
  timeout = 300_000,
): Promise<void> => {
  const row = externalIpPoolRow(page, name);
  try {
    await expect(row.getByText(status, { exact: true })).toBeVisible({ timeout });
  } catch {
    const observed = (
      await row
        .first()
        .innerText({ timeout: 3_000 })
        .catch(() => '')
    )
      .replace(/\s+/g, ' ')
      .trim();
    throw new Error(
      `External IP pool "${name}" did not reach status "${status}" within ${timeout / 1_000} seconds. ` +
        `The row currently reads: "${observed || 'unknown'}".`,
    );
  }
};

/** Verify an External IP pool is absent, failing with a duplicate-pool message. */
export const expectExternalIpPoolNotListed = async (page: Page, name: string): Promise<void> => {
  await waitForExternalIpPoolList(page);
  const row = externalIpPoolRow(page, name);
  const matchingRows = await row.count();
  if (matchingRows > 0) {
    throw new Error(
      `External IP pool "${name}" already exists (${matchingRows} matching rows). ` +
        'Delete it, or run with a different OSAC_E2E_INSTANCE.',
    );
  }
};

/**
 * Delete an External IP pool row when it exists; a missing pool is not an error.
 * A pool that the provider refuses to delete (for example because a tenant's
 * `default-nat` address still references it) is reported and retained rather
 * than failing the run.
 */
export const deleteExternalIpPoolIfPresent = async (
  page: Page,
  name: string,
  expectedId?: string,
): Promise<boolean> => {
  await openExternalIpPoolListPage(page);
  await waitForExternalIpPoolList(page);
  const row = externalIpPoolRow(page, name);
  if ((await row.count()) === 0) {
    // A missing row is only absence when the recorded pool is gone too.
    if (expectedId && (await externalIpPoolExistsById(page, expectedId))) {
      throw new Error(
        `Pool "${name}" exists (${expectedId}) but has no visible row in the pool ` +
          'list, so it cannot be deleted from here. Delete it through another interface.',
      );
    }
    logProgress('external-ip-delete', `Pool "${name}" is already absent.`);
    return false;
  }
  if (expectedId) {
    // Re-check identity immediately before deleting, as for the External IP.
    const currentId = await readExternalIpPoolId(page, name);
    if (currentId !== expectedId) {
      logProgress(
        'external-ip-delete',
        `Pool "${name}" changed identity before deletion ` +
          `(${currentId ?? 'absent'}); leaving it in place.`,
      );
      return false;
    }
  }

  await rowActionsToggle(page, name).click();
  await page.getByRole(...ExternalIpPoolsPage.deleteMenuItem).click();
  const dialog = page.locator(...ExternalIpDeleteDialog.dialog);
  await expect(dialog).toBeVisible();
  await expect(page.locator(...ExternalIpDeleteDialog.title)).toContainText(`Delete ${name}?`);
  await expect(page.getByText(...ExternalIpPoolsPage.deleteWarning)).toBeVisible();
  const refused = page.getByText(...ExternalIpPoolsPage.deleteError);
  const deleteOutcome = page
    .waitForResponse(
      (response) => /\/api\/fulfillment\/[\w.]*\.ExternalIPPools\/Delete$/.test(response.url()),
      { timeout: 60_000 },
    )
    .then(
      (response) => ({ response }),
      (error: unknown) => ({ error }),
    );
  const outcomeRef: { value: 'deleted' | 'refused' | 'timeout' } = { value: 'timeout' };
  const blocked = await withDeleteGuard(
    page,
    /\/api\/fulfillment\/[\w.]*\.ExternalIPPools\/Delete$/,
    expectedId,
    async () => {
      await page.getByRole(...ExternalIpPoolsPage.confirmDelete).click();
      outcomeRef.value = await Promise.race([
        dialog.waitFor({ state: 'hidden', timeout: 60_000 }).then(() => 'deleted' as const),
        refused.waitFor({ state: 'visible', timeout: 60_000 }).then(() => 'refused' as const),
      ]).catch(() => 'timeout' as const);
    },
  );
  if (blocked) {
    throw new Error(blocked);
  }

  if (outcomeRef.value !== 'deleted') {
    // The dialog shows the same alert for every mutation error, so the
    // diagnostic read is bounded and the retention path needs the provider's
    // own wording; anything else must fail the step.
    const alert = page
      .locator('[class*="c-alert"]')
      .filter({ hasText: ExternalIpPoolsPage.deleteError[0] })
      .first();
    const detail = (await alert.innerText({ timeout: 3_000 }).catch(() => ''))
      .replace(/\s+/g, ' ')
      .trim();

    await page
      .getByRole('button', { name: 'Cancel', exact: true })
      .click({ timeout: 5_000 })
      .catch(() => {});

    if (allocatedAddressRefusal.test(detail)) {
      logProgress(
        'external-ip-delete',
        `Pool "${name}" was retained (${detail}). ` +
          "An External IP still references it. Identify that address (a tenant's " +
          'default-nat is one such case, and default networking resources cannot be ' +
          'deleted directly) and remove its tenant or the address if it is ' +
          'deletable, then retry; or delete the pool by hand.',
      );
      return false;
    }

    throw new Error(
      `Deleting pool "${name}" failed: ` +
        `${detail || 'the delete did not settle within 60 seconds'}. ` +
        'Only an allocated-address refusal is a documented retained outcome.',
    );
  }

  const settled = await deleteOutcome;
  if ('error' in settled) {
    throw new Error(`Deleting pool "${name}" produced no response.`);
  }
  if (!settled.response.ok()) {
    throw new Error(`Deleting pool "${name}" did not succeed (HTTP ${settled.response.status()}).`);
  }
  await expect(row).toHaveCount(0, { timeout: 300_000 });
  if (expectedId && (await externalIpPoolExistsById(page, expectedId))) {
    throw new Error(`Pool "${name}" still exists after a successful delete response.`);
  }
  return true;
};
