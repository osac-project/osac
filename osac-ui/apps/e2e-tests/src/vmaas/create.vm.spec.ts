import { expect, test } from '@playwright/test';
import { access } from 'node:fs/promises';

import {
  catalogTestData,
  instanceTypeTestData,
  storageTierTestData,
  tenantNameOrSkip,
  vmTestName,
} from './test_data';
import { VmCreatePage } from '../../../../libs/locators/vms';
import {
  launchCatalogItem,
  openCatalogItemDetails,
} from '../../../../libs/test-utils/src/catalog.page';
import { selectOption } from '../../../../libs/test-utils/src/general/form_controls';
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
    openAndLogin(page, '/', tenantCredentials.username, tenantCredentials.password, 'tenant_admin'),
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
      .locator(...VmCreatePage.catalogItemCards)
      .filter({ hasText: catalogTestData.name })
      .first();
    await expect(catalogCard.locator(...VmCreatePage.catalogItemSelection)).toBeChecked();
    await page.getByRole(...VmCreatePage.next).click();
  });

  const projectName = await testStep('catalog-launch', 'Set the project and VM name', async () => {
    const selectedProject = await selectOption(page, VmCreatePage.project, /.*/);
    await page.locator(...VmCreatePage.vmName).fill(vmName);
    await page.getByRole(...VmCreatePage.next).click();
    return selectedProject;
  });

  await testStep(
    'catalog-launch',
    'Verify the Disk image field on the Compute configuration step',
    async () => {
      const diskImage = page.locator(...VmCreatePage.diskImage);
      await expect(diskImage).toBeVisible();
      if (await diskImage.isEnabled()) {
        await selectOption(page, VmCreatePage.diskImage, catalogTestData.fields.diskImage.name);
      }
    },
  );

  await testStep('catalog-launch', 'Select the Instance type', async () => {
    await selectOption(page, VmCreatePage.instanceType, new RegExp(instanceTypeTestData.name));
    await page.getByRole(...VmCreatePage.next).click();
  });

  await testStep('catalog-launch', 'Configure boot and additional disks', async () => {
    await page.locator(...VmCreatePage.bootDisk).fill('100');
    await selectOption(page, VmCreatePage.bootStorageTier, storageTierTestData.name);
    await page.getByRole(...VmCreatePage.addDisk).click();
    await page.locator(...VmCreatePage.additionalDiskSize).fill('30');
    await selectOption(page, VmCreatePage.storageTier, storageTierTestData.name);
    await page.getByRole(...VmCreatePage.next).click();
  });

  await testStep('catalog-launch', 'Configure default networking and external IP', async () => {
    await expect(page.locator(...VmCreatePage.useTenantDefaultNetwork)).toBeChecked();
    await page.locator(...VmCreatePage.autoExternalIp).check();
    await page.getByRole(...VmCreatePage.next).click();
  });

  const sshKeyName = await testStep(
    'catalog-launch',
    'Select an SSH key and enter cloud-init user-data',
    async () => {
      const selectedSshKey = await selectOption(
        page,
        VmCreatePage.sshPublicKey,
        /^(?!No secret available$).+/,
      );
      await page.locator(...VmCreatePage.cloudInit).fill(cloudInit);
      await page.getByRole(...VmCreatePage.next).click();
      return selectedSshKey;
    },
  );

  await testStep('catalog-launch', 'Verify the VM review page', async () => {
    await expect(page.getByRole(...VmCreatePage.configurationHeading)).toBeVisible();
    await expect(page.getByRole(...VmCreatePage.storageHeading)).toBeVisible();
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
    await expect(page.getByRole(...VmCreatePage.create)).toBeVisible();
  });

  await testStep('catalog-launch', `Submit the VM "${vmName}"`, async () => {
    await page.getByRole(...VmCreatePage.create).click();
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
