import { expect, test } from '@playwright/test';
import { fileURLToPath } from 'node:url';

import { waitForVmToBeDeleted } from '../../../libs/test-utils/src/vms.page';

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

test('waits for the VM list to finish loading', async ({ page }) => {
  let requests = 0;
  await page.route('**/vms?*', async (route) => {
    requests += 1;
    await route.fulfill({
      body: listPage(requests === 1 ? 'Loading' : vmTable(false)),
      contentType: 'text/html',
    });
  });
  await waitForVmToBeDeleted(page, 'test-vm', 5_000);
  expect(requests).toBeGreaterThan(1);
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
  test.beforeAll(() => {
    process.env.OSAC_CLI_PATH = fileURLToPath(new URL('./mock-osac.sh', import.meta.url));
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
