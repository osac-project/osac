import { type Page, expect } from '@playwright/test';

import { VmCreatePage } from '../../../../../libs/locators/vms';
import {
  launchCatalogItem,
  openCatalogItemDetails,
} from '../../../../../libs/test-utils/src/catalog.page';
import { selectOption } from '../../../../../libs/test-utils/src/general/form_controls';
import { openAndLogin } from '../../../../../libs/test-utils/src/general/open_and_login';
import { logProgress, testStep } from '../../../../../libs/test-utils/src/general/test_progress';
import { openCatalogPage } from '../../../../../libs/test-utils/src/main.page';
import {
  readTenantCredentials,
  tenantCredentialsFilePath,
} from '../../../../../libs/test-utils/src/tenants.page';
import {
  deleteVm,
  expectVmDetailsPage,
  listVmNames,
  openVmDetailsPage,
  openVmsPage,
  waitForVmConditionStatus,
  waitForVmToBeDeleted,
  waitForVmToBeRunning,
} from '../../../../../libs/test-utils/src/vms.page';
import {
  catalogTestData,
  instanceTypeTestData,
  storageTierTestData,
  tenantNameOrSkip,
  vmTestName,
} from '../test_data';

const cloudInit = '#cloud-config\nruncmd:\n  - echo vm-e2e';

/** Create the VM used by one resize scenario from the resize catalog blocks. */
export const createResizeVm = async (page: Page): Promise<void> => {
  const tenantName = tenantNameOrSkip();
  if (!tenantName) {
    return;
  }

  const tenantCredentials = await testStep(
    'resize-vm-create',
    'Read generated tenant credentials',
    () => readTenantCredentials(tenantCredentialsFilePath(tenantName)),
  );

  await testStep('resize-vm-create', 'Log in as the tenant admin', () =>
    openAndLogin(page, '/', tenantCredentials.username, tenantCredentials.password, 'tenant_admin'),
  );
  await testStep('resize-vm-create', 'Open the tenant Catalog', () => openCatalogPage(page));
  await testStep('resize-vm-create', `Open catalog item "${catalogTestData.name}"`, () =>
    openCatalogItemDetails(page, catalogTestData.name),
  );
  await testStep('resize-vm-create', 'Launch the VM creation wizard', () =>
    launchCatalogItem(page),
  );

  await testStep('resize-vm-create', 'Select the catalog item', async () => {
    const catalogCard = page
      .locator(...VmCreatePage.catalogItemCards)
      .filter({ hasText: catalogTestData.name })
      .first();
    await expect(catalogCard.locator(...VmCreatePage.catalogItemSelection)).toBeChecked();
    await page.getByRole(...VmCreatePage.next).click();
  });

  const projectName = await testStep(
    'resize-vm-create',
    'Set the project and VM name',
    async () => {
      const selectedProject = await selectOption(page, VmCreatePage.project, /.*/);
      await page.locator(...VmCreatePage.vmName).fill(vmTestName);
      await page.getByRole(...VmCreatePage.next).click();
      return selectedProject;
    },
  );

  await testStep('resize-vm-create', 'Configure the disk image', async () => {
    const diskImage = page.locator(...VmCreatePage.diskImage);
    await expect(diskImage).toBeVisible();
    if (await diskImage.isEnabled()) {
      await selectOption(page, VmCreatePage.diskImage, catalogTestData.fields.diskImage.name);
    }
  });

  await testStep('resize-vm-create', 'Configure the base instance type', async () => {
    await selectOption(page, VmCreatePage.instanceType, new RegExp(instanceTypeTestData.name));
    await page.getByRole(...VmCreatePage.next).click();
  });

  await testStep('resize-vm-create', 'Configure the VM disks', async () => {
    await page.locator(...VmCreatePage.bootDisk).fill('100');
    await selectOption(page, VmCreatePage.bootStorageTier, storageTierTestData.name);
    await page.getByRole(...VmCreatePage.addDisk).click();
    await page.locator(...VmCreatePage.additionalDiskSize).fill('30');
    await selectOption(page, VmCreatePage.storageTier, storageTierTestData.name);
    await page.getByRole(...VmCreatePage.next).click();
  });

  await testStep('resize-vm-create', 'Configure default networking', async () => {
    await expect(page.locator(...VmCreatePage.useTenantDefaultNetwork)).toBeChecked();
    await page.locator(...VmCreatePage.autoExternalIp).check({ force: true });
    await page.getByRole(...VmCreatePage.next).click();
  });

  const sshKeyName = await testStep(
    'resize-vm-create',
    'Select the SSH key and configure cloud-init',
    async () => {
      const selectedSshKey = await selectOption(
        page,
        VmCreatePage.sshPublicKey,
        /^(?!No secret available$).+$/,
      );
      await page.locator(...VmCreatePage.cloudInit).fill(cloudInit);
      await page.getByRole(...VmCreatePage.next).click();
      return selectedSshKey;
    },
  );

  await testStep('resize-vm-create', 'Verify the VM review page', async () => {
    await expect(page.getByRole(...VmCreatePage.configurationHeading)).toBeVisible();
    await expect(page.getByRole(...VmCreatePage.storageHeading)).toBeVisible();
    await expect(page.getByText(catalogTestData.name, { exact: true })).toBeVisible();
    await expect(page.getByText(projectName, { exact: true })).toBeVisible();
    await expect(page.getByText(vmTestName, { exact: true })).toBeVisible();
    await expect(
      page.getByText(catalogTestData.fields.diskImage.name, { exact: true }),
    ).toBeVisible();
    await expect(page.getByText(new RegExp(instanceTypeTestData.name))).toBeVisible();
    await expect(page.getByText(sshKeyName, { exact: true })).toBeVisible();
    await expect(page.getByText('Tenant default', { exact: true })).toBeVisible();
    await expect(page.getByText('Yes', { exact: true })).toBeVisible();
    await expect(
      page.getByText(`100 GB, ${storageTierTestData.name}`, { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText(`30 GB, ${storageTierTestData.name}`, { exact: true }),
    ).toBeVisible();
    await expect(page.getByText(...VmCreatePage.userDataProvided)).toBeVisible();
  });

  await testStep('resize-vm-create', `Create VM "${vmTestName}"`, async () => {
    await page.getByRole(...VmCreatePage.create).click();
    await expectVmDetailsPage(page, vmTestName);
  });
  await testStep('resize-vm-create', `Wait for VM "${vmTestName}" to be Running`, () =>
    waitForVmToBeRunning(page, vmTestName),
  );
  await testStep(
    'resize-vm-create',
    `Wait for VM "${vmTestName}" configuration to be applied`,
    () => waitForVmConditionStatus(page, vmTestName, 'CONFIGURATION APPLIED', 'True'),
  );
};

/** Delete the VM created for the current resize scenario. */
export const deleteResizeVm = async (page: Page): Promise<void> => {
  // The details page is the most reliable cleanup location. The VM list can
  // briefly report an API error while the controller is reconciling a recent
  // restart, even though the VM details page is still available.
  if (/\/vms\/[^/]+$/.test(new URL(page.url()).pathname)) {
    await testStep('resize-vm-delete', `Delete VM "${vmTestName}"`, () =>
      deleteVm(page, vmTestName),
    );
    await testStep('resize-vm-delete', `Verify VM "${vmTestName}" is deleted`, () =>
      waitForVmToBeDeleted(page, vmTestName),
    );
    return;
  }

  await testStep('resize-vm-delete', 'Open the tenant Virtual Machines page', () =>
    openVmsPage(page),
  );
  const vmNames = await testStep('resize-vm-delete', 'Find the scenario VM', () =>
    listVmNames(page),
  );
  if (!vmNames.includes(vmTestName)) {
    logProgress('resize-vm-delete', `VM "${vmTestName}" is already absent.`);
    return;
  }

  await testStep('resize-vm-delete', `Open VM "${vmTestName}"`, () =>
    openVmDetailsPage(page, vmTestName),
  );
  await testStep('resize-vm-delete', `Delete VM "${vmTestName}"`, () => deleteVm(page, vmTestName));
  await testStep('resize-vm-delete', `Verify VM "${vmTestName}" is deleted`, () =>
    waitForVmToBeDeleted(page, vmTestName),
  );
};
