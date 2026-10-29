import { type Page, expect, test } from '@playwright/test';
import { access } from 'node:fs/promises';

import {
  catalogTestData,
  instanceTypeTestData,
  storageTierTestData,
  tenantNameOrSkip,
  vmTestName,
} from './test_data';
import {
  launchCatalogItem,
  openCatalogItemDetails,
} from '../../../../libs/test-utils/src/catalog.page';
import { openAndLogin } from '../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../libs/test-utils/src/general/test_progress';
import { openCatalogPage } from '../../../../libs/test-utils/src/main.page';
import {
  readTenantCredentials,
  tenantCredentialsFilePath,
} from '../../../../libs/test-utils/src/tenants.page';
import {
  expectVmDetailsPage,
  expectVmDetailsToBeRunning,
  expectVmToBeRunning,
  openVmDetailsPage,
  openVmsPage,
  waitForVmToBeRunning,
} from '../../../../libs/test-utils/src/vms.page';

const vmName = vmTestName;
const cloudInit = '#cloud-config\nruncmd:\n  - echo vm-e2e';

const selectOption = async (
  page: Page,
  label: string,
  optionName: string | RegExp,
  index = 0,
): Promise<string> => {
  const field = page.getByLabel(label, { exact: true }).nth(index);
  await expect(field).toBeVisible();
  await field.click();
  const option = page.getByRole('option', { name: optionName }).first();
  await expect(option).toBeVisible();
  const selectedName = (await option.innerText()).trim();
  await option.click();
  return selectedName;
};

test('tenant creates a VM from the published catalog item', async ({ page }) => {
  test.setTimeout(300_000);
  const tenantName = tenantNameOrSkip();
  if (!tenantName) {
    logProgress('catalog-launch', 'Skipped: tenant_name is not configured.');
    return;
  }

  const credentialsFile = tenantCredentialsFilePath(tenantName);
  try {
    await access(credentialsFile);
  } catch {
    test.skip(
      true,
      `Tenant credentials were not found at ${credentialsFile}; run the full flow first.`,
    );
    return;
  }

  logProgress('catalog-launch', `Starting flow for "${catalogTestData.name}".`);
  const tenantCredentials = await testStep(
    'catalog-launch',
    'Read generated tenant credentials',
    () => readTenantCredentials(credentialsFile),
  );

  await testStep('catalog-launch', 'Log in as the tenant user', () =>
    openAndLogin(page, '/', tenantCredentials.username, tenantCredentials.password, 'tenant_type'),
  );

  await testStep('catalog-launch', 'Open the tenant Catalog', () => openCatalogPage(page));
  await testStep(
    'catalog-launch',
    `Select the published VM catalog item "${catalogTestData.name}"`,
    () => openCatalogItemDetails(page, catalogTestData.name),
  );
  await testStep('catalog-launch', 'Click Launch instance', () => launchCatalogItem(page));

  await testStep('catalog-launch', 'Verify the Catalog item wizard step', async () => {
    const catalogCard = page
      .locator('[id^="catalog-item-card-"]')
      .filter({ hasText: catalogTestData.name })
      .first();
    await expect(catalogCard.locator('input[type="radio"]')).toBeChecked();
    await page.getByRole('button', { name: 'Next', exact: true }).click();
  });

  const projectName = await testStep('catalog-launch', 'Set the project and VM name', async () => {
    const selectedProject = await selectOption(page, 'Project', /.*/);
    await page.getByRole('textbox', { name: 'Name', exact: true }).fill(vmName);
    await page.getByRole('button', { name: 'Next', exact: true }).click();
    return selectedProject;
  });

  await testStep('catalog-launch', 'Verify the Disk image wizard step', async () => {
    const diskImage = page.getByLabel('Disk image', { exact: true });
    await expect(diskImage).toBeVisible();
    await expect(diskImage).toBeDisabled();
    await page.getByRole('button', { name: 'Next', exact: true }).click();
  });

  await testStep('catalog-launch', 'Select the Instance type', async () => {
    await selectOption(page, 'Instance type', new RegExp(instanceTypeTestData.name));
    await page.getByRole('button', { name: 'Next', exact: true }).click();
  });

  await testStep('catalog-launch', 'Configure boot and additional disks', async () => {
    await page.getByLabel('Boot disk', { exact: true }).fill('100');
    await selectOption(page, 'Storage tier', storageTierTestData.name);
    await page.getByRole('button', { name: 'Add disk', exact: true }).click();
    await page.getByLabel('Size (GiB)', { exact: true }).fill('30');
    await selectOption(page, 'Storage tier', storageTierTestData.name, 1);
    await page.getByRole('button', { name: 'Next', exact: true }).click();
  });

  await testStep('catalog-launch', 'Configure default networking and external IP', async () => {
    await expect(page.getByLabel('Use tenant default network', { exact: true })).toBeChecked();
    await page.getByLabel('Auto External IP Attachment', { exact: true }).check();
    await page.getByRole('button', { name: 'Next', exact: true }).click();
  });

  const sshKeyName = await testStep(
    'catalog-launch',
    'Select an SSH key and enter cloud-init user-data',
    async () => {
      const selectedSshKey = await selectOption(
        page,
        'SSH public key',
        /^(?!No secret available$).+/,
      );
      await page.getByLabel('Cloud-init', { exact: true }).fill(cloudInit);
      await page.getByRole('button', { name: 'Next', exact: true }).click();
      return selectedSshKey;
    },
  );

  await testStep('catalog-launch', 'Verify the VM review page', async () => {
    await expect(page.getByRole('heading', { name: 'Configuration', exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Storage', exact: true })).toBeVisible();
    await expect(page.getByText(catalogTestData.name, { exact: true })).toBeVisible();
    await expect(page.getByText(projectName, { exact: true })).toBeVisible();
    await expect(page.getByText(vmName, { exact: true })).toBeVisible();
    await expect(
      page.getByText(catalogTestData.fields.diskImage.name, { exact: true }),
    ).toBeVisible();
    await expect(page.getByText(new RegExp(instanceTypeTestData.name))).toBeVisible();
    await expect(page.getByText(sshKeyName, { exact: true })).toBeVisible();
    await expect(page.getByText('Tenant default', { exact: true })).toBeVisible();
    await expect(page.getByText('Yes', { exact: true })).toBeVisible();
    await expect(
      page.getByText('100 GB, ' + storageTierTestData.name, { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText('30 GB, ' + storageTierTestData.name, { exact: true }),
    ).toBeVisible();
    await expect(page.getByText('••••••••', { exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Create', exact: true })).toBeVisible();
  });

  await testStep('catalog-launch', `Submit the VM "${vmName}"`, async () => {
    await page.getByRole('button', { name: 'Create', exact: true }).click();
    await expectVmDetailsPage(page, vmName);
  });

  await testStep('catalog-launch', `Verify the VM "${vmName}" reaches Running`, () =>
    waitForVmToBeRunning(page, vmName),
  );

  await testStep('catalog-launch', `Verify the VM "${vmName}" appears in the VM list`, async () => {
    await openVmsPage(page);
    await expectVmToBeRunning(page, vmName, 60_000);
  });

  await testStep('catalog-launch', `Verify the VM "${vmName}" details page`, async () => {
    await openVmDetailsPage(page, vmName);
    await expectVmDetailsPage(page, vmName);
    await expectVmDetailsToBeRunning(page);
  });
});
