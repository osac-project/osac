import { expect, test } from '@playwright/test';
import { fileURLToPath } from 'node:url';

import { deleteVmIfPresent, waitForVmToBeDeleted } from '../../../libs/test-utils/src/vms.page';

const inheritedCliConfig = process.env.OSAC_CLI_CONFIG;

const listPage = (body: string): string => `
  <h1>Virtual machines</h1>
  <button id="list-view" aria-pressed="true">List</button>
  ${body}
`;

const vmTable = (withVm: boolean): string => `
  <table role="grid" aria-label="Virtual machines"><tbody>
    <tr><td data-label="Name"><a>other-vm</a></td></tr>
    ${withVm ? '<tr><td data-label="Name"><a>test-vm</a></td></tr>' : ''}
  </tbody></table>
`;

test('waits for the VM to disappear from a loaded list', async ({ page }) => {
  let requests = 0;
  await page.route('**/vms?*', async (route) => {
    requests += 1;
    await route.fulfill({ body: listPage(vmTable(requests === 1)), contentType: 'text/html' });
  });
  await waitForVmToBeDeleted(page, 'test-vm', 5_000);
  expect(requests).toBeGreaterThan(1);
});

test('does not report deletion when the VM list never finishes loading', async ({ page }) => {
  await page.route('**/vms?*', (route) =>
    route.fulfill({ body: listPage('Loading'), contentType: 'text/html' }),
  );
  await expect(waitForVmToBeDeleted(page, 'test-vm', 2_000)).rejects.toThrow();
});

test('lets the current VM list finish loading without another navigation', async ({ page }) => {
  let requests = 0;
  await page.route('**/vms?*', async (route) => {
    requests += 1;
    await route.fulfill({
      body: listPage(`<div id="list">Loading</div><script>
        setTimeout(() => { document.getElementById('list').innerHTML = ${JSON.stringify(vmTable(false))}; }, 1500);
      </script>`),
      contentType: 'text/html',
    });
  });
  await waitForVmToBeDeleted(page, 'test-vm', 5_000);
  expect(requests).toBe(1);
});

test('verifies absence without filters when the initial list hides the VM', async ({ page }) => {
  const navigation = `<nav aria-label="Primary navigation"><button aria-expanded="true">Services</button>
    <a href="/vms?search=hidden">Virtual Machines</a></nav>`;
  await page.route('**/catalog', (route) =>
    route.fulfill({ body: navigation, contentType: 'text/html' }),
  );
  await page.route('**/vms?*', (route) =>
    route.fulfill({
      body: listPage(
        route.request().url().includes('search=hidden')
          ? '<p>No virtual machines match your filters</p>'
          : '<h2>No virtual machines found</h2>',
      ),
      contentType: 'text/html',
    }),
  );
  await page.goto('/catalog');
  expect(await deleteVmIfPresent(page, 'test-vm')).toBe(false);
  expect(new URL(page.url()).searchParams.has('project')).toBe(true);
});

test('waits for a VM hidden by the initial filter to disappear', async ({ page }) => {
  let unfilteredRequests = 0;
  const navigation = `<nav aria-label="Primary navigation"><button aria-expanded="true">Services</button>
    <a href="/vms?search=hidden">Virtual Machines</a></nav>`;
  await page.route('**/catalog', (route) =>
    route.fulfill({ body: navigation, contentType: 'text/html' }),
  );
  await page.route('**/vms?*', (route) => {
    const filtered = route.request().url().includes('search=hidden');
    if (!filtered) {
      unfilteredRequests += 1;
    }
    return route.fulfill({
      body: listPage(
        filtered
          ? '<p>No virtual machines match your filters</p>'
          : vmTable(unfilteredRequests === 1),
      ),
      contentType: 'text/html',
    });
  });
  await page.goto('/catalog');
  expect(await deleteVmIfPresent(page, 'test-vm')).toBe(false);
  expect(unfilteredRequests).toBe(2);
});

test('retries an interrupted navigation', async ({ page }) => {
  let attempts = 0;
  const goto = page.goto.bind(page);
  page.goto = (async (...args: Parameters<typeof page.goto>) => {
    attempts += 1;
    if (attempts === 1) {
      throw new Error('page.goto: net::ERR_ABORTED; navigation interrupted');
    }
    return goto(...args);
  }) as typeof page.goto;
  await page.route('**/vms?*', (route) =>
    route.fulfill({ body: listPage('<h2>No virtual machines found</h2>') }),
  );
  await waitForVmToBeDeleted(page, 'test-vm', 5_000);
  expect(attempts).toBe(2);
});

test('retries a navigation replaced by another navigation', async ({ page }) => {
  let attempts = 0;
  const goto = page.goto.bind(page);
  page.goto = (async (...args: Parameters<typeof page.goto>) => {
    attempts += 1;
    if (attempts === 1) {
      throw new Error('Navigation to "/vms" is interrupted by another navigation to "/callback"');
    }
    return goto(...args);
  }) as typeof page.goto;
  await page.route('**/vms?*', (route) =>
    route.fulfill({ body: listPage('<h2>No virtual machines found</h2>') }),
  );
  await waitForVmToBeDeleted(page, 'test-vm', 5_000);
  expect(attempts).toBe(2);
});

test('does not retry other navigation failures', async ({ page }) => {
  page.goto = (() =>
    Promise.reject(new Error('page.goto: net::ERR_NAME_NOT_RESOLVED'))) as typeof page.goto;
  await expect(waitForVmToBeDeleted(page, 'test-vm', 5_000)).rejects.toThrow(
    'ERR_NAME_NOT_RESOLVED',
  );
});

test('recovers from a transient redirect before verifying the list', async ({ page }) => {
  let requests = 0;
  await page.route('**/vms?*', async (route) => {
    requests += 1;
    if (requests === 1) {
      await route.fulfill({
        body: '<script>location.replace("/catalog")</script>',
        contentType: 'text/html',
      });
    } else {
      await route.fulfill({ body: listPage(vmTable(false)), contentType: 'text/html' });
    }
  });
  await page.route('**/catalog', (route) => route.fulfill({ body: '<h1>Catalog</h1>' }));
  await waitForVmToBeDeleted(page, 'test-vm', 12_000);
  expect(requests).toBeGreaterThan(1);
});

test('fails when a redirect persists', async ({ page }) => {
  await page.route('**/vms?*', (route) =>
    route.fulfill({
      body: '<script>location.replace("/catalog")</script>',
      contentType: 'text/html',
    }),
  );
  await page.route('**/catalog', (route) => route.fulfill({ body: '<h1>Catalog</h1>' }));
  await expect(waitForVmToBeDeleted(page, 'test-vm', 7_000)).rejects.toThrow();
});

test('fails when the list reports an API error', async ({ page }) => {
  await page.route('**/vms?*', (route) =>
    route.fulfill({ body: listPage('<h2>An error occurred</h2>') }),
  );
  await expect(waitForVmToBeDeleted(page, 'test-vm', 2_000)).rejects.toThrow('API error');
});

test('does not treat a filtered empty list as deletion', async ({ page }) => {
  await page.route('**/vms?*', (route) =>
    route.fulfill({ body: listPage('<p>No virtual machines match your filters</p>') }),
  );
  await expect(waitForVmToBeDeleted(page, 'test-vm', 2_000)).rejects.toThrow('filtered');
});

test('fails when the VM remains in the list', async ({ page }) => {
  await page.route('**/vms?*', (route) => route.fulfill({ body: listPage(vmTable(true)) }));
  await expect(waitForVmToBeDeleted(page, 'test-vm', 2_000)).rejects.toThrow();
});

test('accepts the unfiltered empty state', async ({ page }) => {
  await page.route('**/vms?*', (route) =>
    route.fulfill({ body: listPage('<h2>No virtual machines found</h2>') }),
  );
  await waitForVmToBeDeleted(page, 'test-vm', 2_000);
});

test.describe('independent API deletion check', () => {
  let originalCliConfig: string | undefined;
  test.beforeAll(() => {
    originalCliConfig = process.env.OSAC_CLI_CONFIG;
    delete process.env.OSAC_CLI_CONFIG;
    process.env.OSAC_CLI_PATH = fileURLToPath(new URL('./mock-osac.sh', import.meta.url));
  });

  test.afterAll(() => {
    if (originalCliConfig === undefined) {
      delete process.env.OSAC_CLI_CONFIG;
    } else {
      process.env.OSAC_CLI_CONFIG = originalCliConfig;
    }
  });

  test.afterEach(() => {
    delete process.env.OSAC_CLI_TEST_OUTPUT;
    delete process.env.OSAC_CLI_TEST_FAIL;
  });

  test('accepts a successful empty result', async () => {
    const { expectComputeInstanceDeleted } =
      await import('../../../libs/osac-cli/src/compute_instance');
    await expectComputeInstanceDeleted('test-tenant', 'test-vm', 2_000);
  });

  test('fails if the API still returns the VM', async () => {
    process.env.OSAC_CLI_TEST_OUTPUT = '[{}]';
    const { expectComputeInstanceDeleted } =
      await import('../../../libs/osac-cli/src/compute_instance');
    await expect(expectComputeInstanceDeleted('test-tenant', 'test-vm', 1_500)).rejects.toThrow();
  });

  test('fails on CLI errors and malformed responses', async () => {
    const { expectComputeInstanceDeleted } =
      await import('../../../libs/osac-cli/src/compute_instance');
    process.env.OSAC_CLI_TEST_FAIL = '1';
    await expect(expectComputeInstanceDeleted('test-tenant', 'test-vm', 1_500)).rejects.toThrow();
    delete process.env.OSAC_CLI_TEST_FAIL;
    process.env.OSAC_CLI_TEST_OUTPUT = '{}';
    await expect(expectComputeInstanceDeleted('test-tenant', 'test-vm', 1_500)).rejects.toThrow(
      'unexpected compute instance list',
    );
  });
});

test('restores the inherited CLI configuration after the API tests', () => {
  expect(process.env.OSAC_CLI_CONFIG).toBe(inheritedCliConfig);
});
